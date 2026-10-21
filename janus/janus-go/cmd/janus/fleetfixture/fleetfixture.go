// Package fleetfixture runs the fleet service in one test process: an embedded NATS
// server, the fleet service on a temporary fleet.db, a tailnet fake, an Agent
// link that serves Agents in the process, the audit service writing into an
// observer, and an in-memory answer for the builds requests the service makes.
// Tests drive the service through app.Fleet, the port the single process uses,
// so they cover the NATS path end to end.
package fleetfixture

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"janus/internal/adapters/agentlinktest"
	"janus/internal/adapters/fleetnats"
	"janus/internal/app"
	contractbuilds "janus/internal/contract/builds"
	contractprojects "janus/internal/contract/projects"
	"janus/internal/platform/natstest"
	"janus/internal/platform/natsx"
	"janus/internal/services/audit"
	"janus/internal/services/fleet"
	"janus/internal/services/fleet/headscale"
	"janus/internal/services/fleet/tailnettest"
)

// Options configures a fixture.
type Options struct {
	// ReconcileInterval is how often the service connects to Agents. Zero means
	// 10 milliseconds, so a test that enrolls an Agent does not wait long.
	ReconcileInterval time.Duration
	// Now is the service's clock. Nil means time.Now.
	Now func() time.Time
	// Tailnet is the control server the service uses. Nil means an in-memory
	// tailnet, which Attach joins Agents to.
	Tailnet headscale.Tailnet
	// Link is the Agent link. Nil means an in-process loopback, which Attach
	// serves Agents on.
	Link fleet.AgentLink
	// ControlURL is the control server's URL that Agents are told to join. Empty
	// means a placeholder.
	ControlURL string
}

// Fixture is a running fleet service and the parts around it.
type Fixture struct {
	t    testing.TB
	logs *observer.ObservedLogs
	// Fleet is the app.Fleet the single process uses, over NATS.
	Fleet app.Fleet
	// Tailnet is the control server the Agents join.
	Tailnet *tailnettest.Fake
	// Link is the Agent link; Attach serves an Agent on it.
	Link *agentlinktest.Loopback
	// Builds answers the builds requests the service makes.
	Builds *Builds
	conn   *nats.Conn
}

// Start runs the fleet service and the audit service on an embedded NATS server
// and returns the fixture. Everything is stopped when the test ends.
func Start(t testing.TB, opts Options) *Fixture {
	t.Helper()
	if opts.ReconcileInterval <= 0 {
		opts.ReconcileInterval = 10 * time.Millisecond
	}
	hub := natstest.Start(t)
	conn := hub.Conn()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	core, logs := observer.New(zapcore.InfoLevel)
	auditService := audit.New(audit.Config{Logger: zap.New(core)})
	if err := auditService.Start(ctx, conn); err != nil {
		t.Fatalf("start the audit service: %v", err)
	}
	t.Cleanup(func() { _ = auditService.Stop(context.Background()) })

	f := &Fixture{t: t, logs: logs, conn: conn}
	tailnet, link := opts.Tailnet, opts.Link
	if tailnet == nil {
		f.Tailnet = tailnettest.NewFake()
		tailnet = f.Tailnet
	}
	if link == nil {
		f.Link = agentlinktest.NewLoopback()
		link = f.Link
	}
	controlURL := opts.ControlURL
	if controlURL == "" {
		controlURL = "https://tailnet.example.test"
	}
	f.Builds = newBuilds(t, conn)
	service := fleet.New(fleet.Config{DatabasePath: t.TempDir() + "/fleet.db", Tailnet: tailnet, ControlURL: controlURL,
		Link: link, ReconcileInterval: opts.ReconcileInterval, Now: opts.Now})
	if err := service.Start(ctx, conn); err != nil {
		t.Fatalf("start the fleet service: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := service.Stop(stopCtx); err != nil {
			t.Errorf("stop the fleet service: %v", err)
		}
		if f.Link != nil {
			if err := f.Link.Close(); err != nil {
				t.Errorf("close the Agent link: %v", err)
			}
		}
	})
	f.Fleet = fleetnats.New(conn)
	return f
}

// Enroll issues an Enrollment Code for owner and trades it for an Agent named
// name, the way `janus-agent enroll` does.
func (f *Fixture) Enroll(owner, name string) app.Enrollment {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code, err := f.Fleet.IssueCode(ctx, owner)
	if err != nil {
		f.t.Fatalf("issue a code: %v", err)
	}
	enrollment, err := f.Fleet.Enroll(ctx, code.Code, name)
	if err != nil {
		f.t.Fatalf("enroll %s: %v", name, err)
	}
	return enrollment
}

// Attach joins the Agent of enrollment to the tailnet as a node named name, and
// serves runtime at the node's address, where the service connects to it. It
// waits until the service has the Agent's first state report.
func (f *Fixture) Attach(owner string, enrollment app.Enrollment, name string, runtime *app.AgentRuntime) (address string) {
	f.t.Helper()
	node, err := f.Tailnet.Join(enrollment.AuthKey, name)
	if err != nil {
		f.t.Fatalf("join the tailnet as %s: %v", name, err)
	}
	address = net.JoinHostPort(node.Addresses[0], strconv.Itoa(fleet.AgentConnectPort))
	f.Link.Serve(address, runtime)
	f.Eventually("the Agent to report its state", func() bool {
		reports, err := f.Fleet.AgentStatuses(context.Background(), owner)
		return err == nil && reports[enrollment.AgentID].Connected
	})
	return address
}

// Eventually waits until done reports true, checking every 10 milliseconds, and
// fails the test when it does not within ten seconds. It is for the states the
// service reaches in the background, such as a Deployment becoming healthy,
// which no event announces.
func (f *Fixture) Eventually(what string, done func() bool) {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			f.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// AuditEvent is one Audit Event the service wrote.
type AuditEvent struct {
	Level  string
	Name   string
	Fields map[string]string
}

// AuditEvents returns the Audit Events written so far, in order.
func (f *Fixture) AuditEvents() []AuditEvent {
	entries := f.logs.All()
	events := make([]AuditEvent, 0, len(entries))
	for _, entry := range entries {
		event := AuditEvent{Level: entry.Level.String(), Name: entry.Message, Fields: map[string]string{}}
		for _, field := range entry.Context {
			event.Fields[field.Key] = field.String
		}
		events = append(events, event)
	}
	return events
}

// HasAudit reports whether an Audit Event with name was written.
func (f *Fixture) HasAudit(name string) bool {
	for _, event := range f.AuditEvents() {
		if event.Name == name {
			return true
		}
	}
	return false
}

// Builds answers the builds requests the fleet service makes, from records the
// test adds. A Build without a Check has none yet.
type Builds struct {
	t  testing.TB
	mu sync.Mutex
	// builds and checks are by Build ID; snapshots by Project ID.
	builds    map[string]contractbuilds.Build
	checks    map[string]contractbuilds.Check
	snapshots map[string][]contractprojects.Snapshot
	conn      *nats.Conn
}

func newBuilds(t testing.TB, conn *nats.Conn) *Builds {
	b := &Builds{t: t, conn: conn, builds: map[string]contractbuilds.Build{}, checks: map[string]contractbuilds.Check{},
		snapshots: map[string][]contractprojects.Snapshot{}}
	answers := map[string]func(contractbuilds.BuildKey) any{
		contractbuilds.SubjectFindBuild:       b.findBuild,
		contractbuilds.SubjectFindCheck:       b.findCheck,
		contractbuilds.SubjectFindArtifact:    b.findArtifact,
		contractbuilds.SubjectRestoreArtifact: func(contractbuilds.BuildKey) any { return contractbuilds.Empty{} },
	}
	for subject, answer := range answers {
		if _, err := conn.Subscribe(subject, func(msg *nats.Msg) {
			var key contractbuilds.BuildKey
			if err := json.Unmarshal(msg.Data, &key); err != nil {
				t.Errorf("builds request %s: %v", msg.Subject, err)
				return
			}
			data, _ := json.Marshal(answer(key))
			_ = msg.Respond(data)
		}); err != nil {
			t.Fatalf("answer %s: %v", subject, err)
		}
	}
	if _, err := conn.Subscribe(contractprojects.SubjectListSnapshots, func(msg *nats.Msg) {
		var key contractprojects.ProjectKey
		if err := json.Unmarshal(msg.Data, &key); err != nil {
			t.Errorf("projects request: %v", err)
			return
		}
		data, _ := json.Marshal(b.listSnapshots(key))
		_ = msg.Respond(data)
	}); err != nil {
		t.Fatalf("answer %s: %v", contractprojects.SubjectListSnapshots, err)
	}
	return b
}

// Add records a Build of owner's. check is its Check, or nil when it has none
// yet. module is the WASM module of its Artifact, which is stored in the
// artifacts store as a ZIP under the Build's ID, or nil for a Build with no
// Artifact.
func (b *Builds) Add(owner string, build contractbuilds.Build, check *contractbuilds.Check, module []byte) {
	b.t.Helper()
	if module == nil {
		b.record(owner, build, check)
		return
	}
	b.AddArtifact(owner, build, check, zipOf(b.t, module), ArtifactName)
}

// AddArtifact records a Build of owner's whose Artifact is data, stored as it is
// under the Build's ID, and named name, which decides how it is read: a name
// ending in .zst is Zstandard compressed.
func (b *Builds) AddArtifact(owner string, build contractbuilds.Build, check *contractbuilds.Check, data []byte, name string) {
	b.t.Helper()
	build.ArtifactName = name
	b.record(owner, build, check)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	js, err := jetstream.New(b.conn)
	if err != nil {
		b.t.Fatal(err)
	}
	store, err := natsx.DeclareObjectStore(ctx, js, natsx.ArtifactsBucket)
	if err != nil {
		b.t.Fatal(err)
	}
	if _, err := store.PutBytes(ctx, build.ID, data); err != nil {
		b.t.Fatal(err)
	}
}

// record keeps a Build and its Check for the answers.
func (b *Builds) record(owner string, build contractbuilds.Build, check *contractbuilds.Check) {
	build.OwnerID = owner
	if build.ArtifactName == "" {
		build.ArtifactName = ArtifactName
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.builds[build.ID] = build
	if check != nil {
		b.checks[build.ID] = *check
	}
}

// AddSnapshot makes snapshotID a ready Snapshot of the Owner's project, of
// language, so the service reads the template limits of that language.
func (b *Builds) AddSnapshot(owner, projectID, snapshotID, language string) {
	runtimes := map[string]string{"Rust": "rust", "Go": "go", "JavaScript": "node", "TypeScript": "node"}
	b.mu.Lock()
	defer b.mu.Unlock()
	key := owner + "/" + projectID
	b.snapshots[key] = append(b.snapshots[key], contractprojects.Snapshot{ID: snapshotID, Language: language, Runtime: runtimes[language], Active: true})
}

func (b *Builds) listSnapshots(key contractprojects.ProjectKey) contractprojects.Snapshots {
	b.mu.Lock()
	defer b.mu.Unlock()
	return contractprojects.Snapshots{Snapshots: b.snapshots[key.OwnerID+"/"+key.ProjectID]}
}

func (b *Builds) findBuild(key contractbuilds.BuildKey) any {
	b.mu.Lock()
	defer b.mu.Unlock()
	build, ok := b.builds[key.BuildID]
	if !ok || build.OwnerID != key.OwnerID {
		return contractbuilds.BuildFound{}
	}
	return contractbuilds.BuildFound{Found: true, Build: build}
}

func (b *Builds) findCheck(key contractbuilds.BuildKey) any {
	b.mu.Lock()
	defer b.mu.Unlock()
	check, ok := b.checks[key.BuildID]
	if !ok || b.builds[key.BuildID].OwnerID != key.OwnerID {
		return contractbuilds.CheckFound{}
	}
	return contractbuilds.CheckFound{Found: true, Check: check}
}

func (b *Builds) findArtifact(key contractbuilds.BuildKey) any {
	b.mu.Lock()
	defer b.mu.Unlock()
	build, ok := b.builds[key.BuildID]
	if !ok || build.OwnerID != key.OwnerID {
		return contractbuilds.ArtifactFound{}
	}
	return contractbuilds.ArtifactFound{Found: true, ObjectName: build.ID, ArtifactName: build.ArtifactName}
}

// ArtifactName is the name of the Artifact ZIP a test Build reports when its
// test does not name one.
const ArtifactName = "app.zip"

// zipOf packs module as the one entry of an Artifact ZIP, the form a build
// produces.
func zipOf(t testing.TB, module []byte) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entry, err := writer.Create("app.wasm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = entry.Write(module); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

// Publish sends event on subject, as another service publishes its events: the
// fleet service consumes the projects service's events from EVENTS.
func (f *Fixture) Publish(subject string, event any) {
	f.t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		f.t.Fatal(err)
	}
	js, err := jetstream.New(f.conn)
	if err != nil {
		f.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := js.PublishMsg(ctx, &nats.Msg{Subject: subject, Data: data}); err != nil {
		f.t.Fatalf("publish %s: %v", subject, err)
	}
}

// Conn returns the connection to the test's NATS server, for a client of another
// service the test runs against the same server.
func (f *Fixture) Conn() *nats.Conn {
	return f.conn
}
