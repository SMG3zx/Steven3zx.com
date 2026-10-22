package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"janus/internal/domain/cgi"
	"janus/internal/domain/usage"
)

// Request limits of a Deployment on an Agent (GLOSSARY.md, Request limits).
const (
	// MaxConcurrentRequests is how many requests an Agent serves at once.
	MaxConcurrentRequests = 16
	// MaxBodyBytes caps request and response bodies.
	MaxBodyBytes = 10 << 20
	// RequestTimeout is the wall-clock limit of one request.
	RequestTimeout = 30 * time.Second
	// maxHeaderBytes is how much a module may write before its body.
	maxHeaderBytes = 64 << 10
	// maxReportBacklog is how many Run reports an Agent holds for the control
	// plane; the oldest is dropped for a new one.
	maxReportBacklog = 1000
)

// The tailnet ports an Agent listens on, and the header that names the
// Deployment a request is for. Only the control plane can reach them.
const (
	// AgentConnectPort carries the control plane's stream (ENG-78).
	AgentConnectPort = 7100
	// AgentServePort answers the edge's HTTP requests for Deployments.
	AgentServePort = 7101
	// DeploymentHeader names the Deployment a request to AgentServePort is for.
	DeploymentHeader = "Janus-Deployment"
)

// Refusals of a request or a command by an Agent.
var (
	ErrAgentPaused        = errors.New("the Agent is paused")
	ErrAgentBusy          = errors.New("the Agent is already serving as many requests as it can")
	ErrDeploymentNotFound = errors.New("this Agent does not serve that Deployment")
	ErrBodyTooLarge       = errors.New("the request body is larger than 10 MiB")
	ErrModuleHash         = errors.New("the module does not match its SHA-256")
)

// ServeRequest is an HTTP request for a Deployment, with its whole body.
type ServeRequest struct {
	cgi.Request
	Body []byte
}

// ServeResponse is the HTTP response to a ServeRequest.
type ServeResponse = cgi.Response

// Runtime is the Janus Agent: it serves the Deployments the control plane
// hands it, one module run per request (ADR 0002), with every run confined to
// the Owner's host folder. It keeps its Deployments and its pause in
// LocalState, so a restart resumes them. It reports the Usage of every run on
// Reports, and signals Changes whenever its AgentState changes.
//
// Reports and Changes are meant for one reader, the connection to the control
// plane. A report that finds the backlog full drops the oldest one.
type Runtime struct {
	runner Runner
	state  LocalState
	folder string
	now    func() time.Time

	slots   chan struct{}
	reports chan RunReport
	changes chan struct{}
	serving sync.WaitGroup
	closing atomic.Bool

	mu          sync.Mutex
	paused      bool
	deployments map[string]HostedDeployment
}

// NewRuntime returns an Runtime that runs modules with runner, keeps
// its state in state and lets modules use the host directory folder. It reads
// the time from now.
func NewRuntime(runner Runner, state LocalState, folder string, now func() time.Time) *Runtime {
	return &Runtime{
		runner: runner, state: state, folder: folder, now: now,
		slots:       make(chan struct{}, MaxConcurrentRequests),
		reports:     make(chan RunReport, maxReportBacklog),
		changes:     make(chan struct{}, 1),
		deployments: map[string]HostedDeployment{},
	}
}

// Start loads the Deployments and the pause the Agent kept, and serves them.
// A kept Deployment whose module no longer matches its hash is not served, and
// the error names it; the others are served all the same.
func (r *Runtime) Start(ctx context.Context) error {
	paused, err := r.state.Paused(ctx)
	if err != nil {
		return fmt.Errorf("read the pause: %w", err)
	}
	kept, err := r.state.Deployments(ctx)
	if err != nil {
		return fmt.Errorf("read the Deployments: %w", err)
	}
	var problems []error
	r.mu.Lock()
	r.paused = paused
	for _, deployment := range kept {
		if hashOf(deployment.Module) != deployment.SHA256 {
			problems = append(problems, fmt.Errorf("Deployment %s: %w", deployment.ID, ErrModuleHash))
			continue
		}
		r.deployments[deployment.ID] = deployment
	}
	r.mu.Unlock()
	r.changed()
	return errors.Join(problems...)
}

// Deploy checks the module against its hash, runs one GET / probe, and when
// the probe ends without a trap, a timeout or running out of fuel, keeps the
// Deployment and serves it. The status the module answers is not judged. A
// Deployment with the same ID is replaced. A paused Agent refuses.
func (r *Runtime) Deploy(ctx context.Context, deployment HostedDeployment) error {
	if r.isPaused() {
		return ErrAgentPaused
	}
	if hashOf(deployment.Module) != deployment.SHA256 {
		return ErrModuleHash
	}
	// The probe is a request like any other, so it waits for one of the 16
	// slots rather than adding a 17th run.
	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	probe := ServeRequest{Request: cgi.Request{Method: "GET", Path: "/", Host: "localhost", RemoteAddr: "127.0.0.1"}}
	_, err := r.run(ctx, deployment, probe, true)
	<-r.slots
	if err != nil {
		return fmt.Errorf("the GET / probe failed: %w", err)
	}
	if err := r.state.SaveDeployment(ctx, deployment); err != nil {
		return fmt.Errorf("keep the Deployment: %w", err)
	}
	r.mu.Lock()
	r.deployments[deployment.ID] = deployment
	r.mu.Unlock()
	r.changed()
	return nil
}

// Stop stops serving a Deployment and forgets it. Stopping one the Agent does
// not serve is not an error.
func (r *Runtime) Stop(ctx context.Context, deploymentID string) error {
	if err := r.state.DeleteDeployment(ctx, deploymentID); err != nil {
		return fmt.Errorf("forget the Deployment: %w", err)
	}
	r.mu.Lock()
	_, served := r.deployments[deploymentID]
	delete(r.deployments, deploymentID)
	r.mu.Unlock()
	if served {
		r.changed()
	}
	return nil
}

// Apply carries out one command from the control plane.
func (r *Runtime) Apply(ctx context.Context, command AgentCommand) error {
	switch {
	case command.Deploy != nil:
		return r.Deploy(ctx, *command.Deploy)
	case command.Stop != "":
		return r.Stop(ctx, command.Stop)
	case command.Pause:
		return r.Pause(ctx)
	case command.Resume:
		return r.Resume(ctx)
	}
	return errors.New("the command has no action")
}

// Pause stops serving every Deployment until Resume. Requests already running
// finish. The pause is kept, so it outlasts a restart.
func (r *Runtime) Pause(ctx context.Context) error { return r.setPaused(ctx, true) }

// Resume serves the Deployments again after Pause.
func (r *Runtime) Resume(ctx context.Context) error { return r.setPaused(ctx, false) }

func (r *Runtime) setPaused(ctx context.Context, paused bool) error {
	if err := r.state.SetPaused(ctx, paused); err != nil {
		return fmt.Errorf("keep the pause: %w", err)
	}
	r.applyPause(paused)
	return nil
}

func (r *Runtime) applyPause(paused bool) {
	r.mu.Lock()
	changed := r.paused != paused
	r.paused = paused
	r.mu.Unlock()
	if changed {
		r.changed()
	}
}

// Refresh reads the pause from LocalState again, so a pause or resume written
// by the Agent's command line takes effect.
func (r *Runtime) Refresh(ctx context.Context) error {
	paused, err := r.state.Paused(ctx)
	if err != nil {
		return err
	}
	r.applyPause(paused)
	return nil
}

// Watch calls Refresh every interval until ctx ends.
func (r *Runtime) Watch(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = r.Refresh(ctx)
		}
	}
}

// State returns whether the Agent is paused and the Deployments it serves.
func (r *Runtime) State() AgentState {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := AgentState{Paused: r.paused, Deployments: make([]DeploymentHash, 0, len(r.deployments))}
	for _, deployment := range r.deployments {
		state.Deployments = append(state.Deployments, DeploymentHash{ID: deployment.ID, SHA256: deployment.SHA256})
	}
	sort.Slice(state.Deployments, func(i, j int) bool { return state.Deployments[i].ID < state.Deployments[j].ID })
	return state
}

// Reports delivers a RunReport for every run, the probes included.
func (r *Runtime) Reports() <-chan RunReport { return r.reports }

// Requeue puts back a report taken from Reports that could not be delivered,
// so the next connection sends it. The control plane keeps a run once, so a
// report that did arrive and is sent again changes nothing.
func (r *Runtime) Requeue(report RunReport) { r.report(report) }

// Changes receives a value after the AgentState changes. Changes that happen
// before it is read are merged into one.
func (r *Runtime) Changes() <-chan struct{} { return r.changes }

// Serve answers one request for a Deployment by running its module. A request
// the Agent refuses before running anything fails with ErrAgentPaused,
// ErrDeploymentNotFound, ErrBodyTooLarge or ErrAgentBusy. Once the module runs,
// Serve always returns a response: when the run fails or hits a limit it is a
// 5xx, and the failure is in the RunReport.
func (r *Runtime) Serve(ctx context.Context, deploymentID string, request ServeRequest) (ServeResponse, error) {
	r.mu.Lock()
	paused := r.paused
	deployment, found := r.deployments[deploymentID]
	r.mu.Unlock()
	switch {
	case r.closing.Load(), paused:
		return ServeResponse{}, ErrAgentPaused
	case !found:
		return ServeResponse{}, ErrDeploymentNotFound
	case len(request.Body) > MaxBodyBytes:
		return ServeResponse{}, ErrBodyTooLarge
	}
	select {
	case r.slots <- struct{}{}:
	default:
		return ServeResponse{}, ErrAgentBusy
	}
	r.serving.Add(1)
	defer func() {
		<-r.slots
		r.serving.Done()
	}()
	response, _ := r.run(ctx, deployment, request, false)
	return response, nil
}

// Drain refuses new requests and waits for the running ones, or until ctx ends.
func (r *Runtime) Drain(ctx context.Context) error {
	r.closing.Store(true)
	done := make(chan struct{})
	go func() { r.serving.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run runs the module once for request and reports it. The error is the run's
// own failure: a trap, a limit, or the runner's refusal of the module.
func (r *Runtime) run(ctx context.Context, deployment HostedDeployment, request ServeRequest, probe bool) (ServeResponse, error) {
	request.ContentLength = len(request.Body)
	limits := deployment.Limits
	limits.Timeout = RequestTimeout
	started := r.now()
	result, err := r.runner.Run(ctx, deployment.Module, RunInput{
		Env: cgi.Env(request.Request), Stdin: request.Body, Dir: r.folder,
		MaxStdout: MaxBodyBytes + maxHeaderBytes, Limits: limits,
	})
	report := RunReport{RunID: uuid.NewString(), DeploymentID: deployment.ID, Usage: result.Usage,
		NetworkBytesIn: messageBytes(request.Header, request.Body), Status: usage.StatusOK, Probe: probe, StartedAt: started.UTC()}
	var response ServeResponse
	var parsed ServeResponse
	var parseErr error
	if err == nil && !result.StdoutTruncated {
		parsed, parseErr = cgi.ParseResponse(result.Stdout)
	}
	switch {
	case errors.Is(err, ErrRunTimeout):
		report.Status, report.Error = usage.StatusTimeout, err.Error()
		response = plainResponse(504, "The application took longer than 30 seconds.")
	case errors.Is(err, ErrOutOfFuel):
		report.Status, report.Error = usage.StatusOutOfFuel, err.Error()
		response = plainResponse(500, "The application ran out of fuel.")
	case err != nil:
		report.Status, report.Error = usage.StatusFailed, err.Error()
		response = plainResponse(500, "The application failed.")
	case result.StdoutTruncated, len(parsed.Body) > MaxBodyBytes:
		report.Status, report.Error = usage.StatusFailed, "the response is larger than 10 MiB"
		response = plainResponse(500, "The application's response is larger than 10 MiB.")
	case parseErr != nil:
		report.Status, report.Error = usage.StatusFailed, parseErr.Error()
		response = plainResponse(502, "The application's response could not be read.")
	default:
		response = parsed
	}
	report.NetworkBytesOut = messageBytes(response.Header, response.Body)
	r.report(report)
	return response, err
}

// messageBytes is the size of an HTTP message's headers and body, which is
// what Usage counts as network bytes: each header as "Name: value\r\n", then
// the body.
func messageBytes(header map[string][]string, body []byte) uint64 {
	n := len(body)
	for name, values := range header {
		for _, value := range values {
			n += len(name) + len(": ") + len(value) + len("\r\n")
		}
	}
	return uint64(n)
}

func plainResponse(status int, text string) ServeResponse {
	return ServeResponse{Status: status, Header: map[string][]string{"Content-Type": {"text/plain; charset=utf-8"}}, Body: []byte(text + "\n")}
}

// report queues a RunReport, dropping the oldest when the backlog is full.
func (r *Runtime) report(report RunReport) {
	for {
		select {
		case r.reports <- report:
			return
		default:
		}
		select {
		case <-r.reports:
		default:
		}
	}
}

// changed signals Changes without blocking.
func (r *Runtime) changed() {
	select {
	case r.changes <- struct{}{}:
	default:
	}
}

func (r *Runtime) isPaused() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paused
}

// hashOf is the lower-case hex SHA-256 of a module.
func hashOf(module []byte) string {
	sum := sha256.Sum256(module)
	return hex.EncodeToString(sum[:])
}
