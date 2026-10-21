package agentgrpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"testing"
	"time"

	"janus/internal/adapters/agentlinktest"
	"janus/internal/adapters/localstatetest"
	"janus/internal/adapters/runnertest"
	"janus/internal/app"
)

func allowAll(context.Context, string) error { return nil }

func dialTCP(ctx context.Context, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "tcp", address)
}

// start serves runtime over gRPC on a loopback port, address when given.
func start(t *testing.T, runtime *app.AgentRuntime, address string, authorize func(context.Context, string) error) (string, func()) {
	t.Helper()
	if address == "" {
		address = "127.0.0.1:0"
	}
	var listener net.Listener
	var err error
	// A restarted Agent takes back its port; the old socket may linger a moment.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if listener, err = net.Listen("tcp", address); err == nil || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(runtime, authorize)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		server.Stop()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	}
	t.Cleanup(stop)
	return listener.Addr().String(), stop
}

func TestGRPCPassesTheAgentLinkContract(t *testing.T) {
	agentlinktest.Run(t, func(*testing.T) agentlinktest.Subject {
		return agentlinktest.Subject{
			Link: NewLink(dialTCP, WithMaxRetryDelay(200*time.Millisecond)),
			Start: func(t *testing.T, runtime *app.AgentRuntime, address string) (string, func()) {
				return start(t, runtime, address, allowAll)
			},
		}
	})
}

func TestServerRefusesACallerItDoesNotAuthorize(t *testing.T) {
	runtime := app.NewAgentRuntime(runnertest.NewFake(), localstatetest.NewMemory(), "", time.Now)
	asked := make(chan string, 16)
	address, _ := start(t, runtime, "", func(_ context.Context, remoteAddr string) error {
		asked <- remoteAddr
		return errors.New("not the control plane")
	})
	link := NewLink(dialTCP, WithMaxRetryDelay(100*time.Millisecond))
	defer link.Close()
	events := make(chan app.AgentEvent, 8)
	link.Connect("agent-1", address, func(event app.AgentEvent) { events <- event })
	// Wait until the Agent has refused the link twice: once it has, the link
	// knows the stream did not come up.
	var caller string
	for range 2 {
		caller = <-asked
	}
	if err := link.Send(context.Background(), "agent-1", app.AgentCommand{Pause: true}); !errors.Is(err, app.ErrAgentUnreachable) {
		t.Fatalf("send through a refused stream: %v", err)
	}
	select {
	case event := <-events:
		t.Fatalf("a refused caller got %+v", event)
	default:
	}
	if runtime.State().Paused {
		t.Fatal("a refused caller paused the Agent")
	}
	if host, _, err := net.SplitHostPort(caller); err != nil || host != "127.0.0.1" {
		t.Fatalf("authorize was asked about %q", caller)
	}
}

func TestDeployCarriesAModuleLargerThanGRPCsDefaultLimit(t *testing.T) {
	runner := runnertest.NewFake()
	module := append([]byte("\x00asm\x01\x00\x00\x00"), make([]byte, 12<<20)...)
	runner.Handle(module, func(app.RunInput) (app.RunResult, error) {
		return app.RunResult{Stdout: []byte("Content-Type: text/plain\n\nbig")}, nil
	})
	runtime := app.NewAgentRuntime(runner, localstatetest.NewMemory(), "", time.Now)
	address, _ := start(t, runtime, "", allowAll)
	link := NewLink(dialTCP)
	defer link.Close()
	link.Connect("agent-1", address, func(app.AgentEvent) {})
	deployment := app.HostedDeployment{ID: "big", SHA256: sha256Hex(module), Module: module, Limits: app.RunLimits{Fuel: 1, MemoryBytes: 1}}
	deadline := time.Now().Add(20 * time.Second)
	for {
		err := link.Send(context.Background(), "agent-1", app.AgentCommand{Deploy: &deployment})
		if err == nil {
			break
		}
		if !errors.Is(err, app.ErrAgentUnreachable) || time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := runtime.State().Deployments; len(got) != 1 || got[0].ID != "big" {
		t.Fatalf("deployments = %+v", got)
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
