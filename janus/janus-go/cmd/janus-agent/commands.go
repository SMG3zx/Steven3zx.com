package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"janus/internal/domain/agent"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"tailscale.com/feature/buildfeatures"

	"janus/internal/agent/disk"
	"janus/internal/agent/edge"
	"janus/internal/agent/link"
	"janus/internal/agent/wasmtime"
	contractfleet "janus/internal/contract/fleet"
	"janus/internal/platform/tailnode"
)

// errHostListener is why an Agent built without the ts_omit_peerapiserver tag
// refuses to join the tailnet.
var errHostListener = errors.New("this janus-agent was built without -tags " + buildTags +
	": it would open tsnet's peerAPI listener on every host interface, and an Agent opens no listener outside the tailnet")

// buildTags are the tags janus-agent must be built with.
const buildTags = "ts_omit_peerapiserver"

// noHostListener refuses a build whose tsnet would listen on the host. tsnet
// in netstack mode opens a kernel TCP listener for its peerAPI on every
// interface, which netstack does not need; the build tag removes it.
func noHostListener() error {
	if buildfeatures.HasPeerAPIServer {
		return errHostListener
	}
	return nil
}

// enroll trades an Enrollment Code for a pre-auth key at the control plane,
// joins the tailnet with it once, and records the Agent's settings.
func enroll(ctx context.Context, out io.Writer, state, server, folder, name, code string) error {
	if err := noHostListener(); err != nil {
		return err
	}
	if server == "" {
		return errors.New("give the control plane's URL with -server")
	}
	abs, err := hostFolder(folder)
	if err != nil {
		return err
	}
	if _, err = os.Stat(settingsPath(state)); err == nil {
		return fmt.Errorf("this Agent is already enrolled (%s exists); remove the state directory to enroll again", settingsPath(state))
	}
	body, err := json.Marshal(map[string]string{"code": code, "name": name})
	if err != nil {
		return err
	}
	server = strings.TrimRight(server, "/")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/api/agents/enroll", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("reach the control plane: %w", err)
	}
	defer response.Body.Close()
	var reply struct {
		Error      string                    `json:"error"`
		AgentID    string                    `json:"agent_id"`
		OwnerID    string                    `json:"owner_id"`
		AuthKey    string                    `json:"auth_key"`
		Tag        string                    `json:"tag"`
		ControlURL string                    `json:"control_url"`
		LeafCreds  []byte                    `json:"leaf_creds"`
		LocalCreds []byte                    `json:"local_creds"`
		Trust      *contractfleet.AgentTrust `json:"trust"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&reply); err != nil {
		return fmt.Errorf("read the control plane's answer (%s): %w", response.Status, err)
	}
	if response.StatusCode != http.StatusCreated {
		if reply.Error == "" {
			reply.Error = response.Status
		}
		return fmt.Errorf("enrollment refused: %s", reply.Error)
	}
	joinCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	node, err := tailnode.Start(joinCtx, tailnode.Config{Dir: tailnetDir(state), Hostname: nodeName(reply.AgentID), ControlURL: reply.ControlURL, AuthKey: reply.AuthKey})
	if err != nil {
		return err
	}
	if err = node.Close(); err != nil {
		return err
	}
	if err = saveCreds(state, leafCreds, reply.LeafCreds); err != nil {
		return err
	}
	if err = saveCreds(state, localCreds, reply.LocalCreds); err != nil {
		return err
	}
	if err = saveSettings(state, settings{AgentID: reply.AgentID, OwnerID: reply.OwnerID, Name: name, Server: server,
		ControlURL: reply.ControlURL, Tag: reply.Tag, Folder: abs, Trust: reply.Trust}); err != nil {
		return err
	}
	fmt.Fprintf(out, "Enrolled %q as Agent %s. Deployments may use %s.\nStart it with: janus-agent run\n", name, reply.AgentID, abs)
	return nil
}

// nodeName is the Agent's hostname on the tailnet.
func nodeName(agentID string) string {
	if len(agentID) > 8 {
		agentID = agentID[:8]
	}
	return "agent-" + agentID
}

// serve runs the Agent until ctx ends: it resumes its kept Deployments, joins
// the tailnet, and listens there, and only there, for the control plane's
// stream and the edge's requests. Both accept only callers tagged
// contract/fleet's ControlTag.
func serve(ctx context.Context, state string) error {
	s, err := loadSettings(state)
	if err != nil {
		return err
	}
	logger := log.New(os.Stderr, "janus-agent ", log.LstdFlags)
	if err = noHostListener(); err != nil {
		return err
	}
	if !wasmtime.Available() {
		return errors.New("this build of janus-agent cannot run WebAssembly: it was built without cgo")
	}
	if _, err = hostFolder(s.Folder); err != nil {
		return err
	}
	disk, err := disk.Open(deploymentsDir(state))
	if err != nil {
		return err
	}
	runtime := agent.NewRuntime(wasmtime.Wasmtime{}, disk, s.Folder, time.Now)
	if err = runtime.Start(ctx); err != nil {
		// A Deployment whose module changed on disk is not served; the rest are.
		logger.Printf("some kept Deployments are not served: %v", err)
	}
	node, err := tailnode.Start(ctx, tailnode.Config{Dir: tailnetDir(state), Hostname: nodeName(s.AgentID), ControlURL: s.ControlURL})
	if err != nil {
		return err
	}
	defer node.Close()
	authorize := node.RequireTag(contractfleet.ControlTag)

	serveListener, err := node.Listen(agent.AgentServePort)
	if err != nil {
		return err
	}
	hub, err := joinHub(ctx, state, s, node)
	if err != nil {
		return err
	}
	defer hub.Close()
	web := &http.Server{Handler: edge.Handler(runtime, authorize), ReadHeaderTimeout: 10 * time.Second}
	failed := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := web.Serve(serveListener); !errors.Is(err, http.ErrServerClosed) {
			failed <- err
		}
	})
	serveCtx, stopServe := context.WithCancel(ctx)
	wg.Go(func() {
		if err := link.Serve(serveCtx, link.Config{Conn: hub.conn, AgentID: s.AgentID, OwnerID: s.OwnerID, Runtime: runtime}); err != nil {
			failed <- err
		}
	})
	watchCtx, stopWatch := context.WithCancel(context.Background())
	wg.Go(func() { runtime.Watch(watchCtx, time.Second) })
	state0 := runtime.State()
	logger.Printf("Agent %s is on the tailnet, serving %d Deployments from %s (paused: %v)", s.AgentID, len(state0.Deployments), s.Folder, state0.Paused)

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-failed:
	}
	logger.Printf("stopping")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), agent.RequestTimeout+5*time.Second)
	defer cancel()
	stopWatch()
	stopServe()
	_ = web.Shutdown(shutdownCtx)
	_ = runtime.Drain(shutdownCtx)
	wg.Wait()
	if serveErr != nil {
		return serveErr
	}
	return nil
}

// setPaused pauses or resumes the Agent through its kept state, which the
// running Agent reads every second.
func setPaused(ctx context.Context, out io.Writer, state string, paused bool) error {
	if _, err := loadSettings(state); err != nil {
		return err
	}
	disk, err := disk.Open(deploymentsDir(state))
	if err != nil {
		return err
	}
	runtime := agent.NewRuntime(nil, disk, "", time.Now)
	if paused {
		if err = runtime.Pause(ctx); err != nil {
			return err
		}
		fmt.Fprintln(out, "Paused: the Agent stops serving within a second. Resume with: janus-agent resume")
		return nil
	}
	if err = runtime.Resume(ctx); err != nil {
		return err
	}
	fmt.Fprintln(out, "Resumed: the Agent serves again within a second.")
	return nil
}

// status prints the Agent's settings, whether it is paused and its Deployments.
func status(ctx context.Context, out io.Writer, state string) error {
	s, err := loadSettings(state)
	if err != nil {
		return err
	}
	disk, err := disk.Open(deploymentsDir(state))
	if err != nil {
		return err
	}
	paused, err := disk.Paused(ctx)
	if err != nil {
		return err
	}
	deployments, err := disk.Deployments(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Agent %s (%s), control plane %s\nHost folder: %s\nPaused: %v\nDeployments: %d\n", s.Name, s.AgentID, s.Server, s.Folder, paused, len(deployments))
	for _, deployment := range deployments {
		fmt.Fprintf(out, "  %s  sha256:%s\n", deployment.ID, deployment.SHA256)
	}
	return nil
}
