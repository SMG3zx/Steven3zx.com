// Package agentgrpc carries the protocol between the control plane and a Janus
// Agent (ENG-78): one gRPC bidirectional stream per Agent, opened by the
// control plane. Server is the Agent's end, which drives an app.AgentRuntime;
// Link is the control plane's end, the Agent link of the fleet service.
package agentgrpc

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"janus/internal/adapters/agentgrpc/agentpb"
	"janus/internal/app"
)

// Server is the Agent's end of the stream. It accepts a caller only when
// authorize allows its address, and serves one stream at a time: a new stream
// replaces the old one, so events never split between two.
type Server struct {
	agentpb.UnimplementedAgentServer
	runtime   *app.AgentRuntime
	authorize func(ctx context.Context, remoteAddr string) error
	grpc      *grpc.Server

	mu      sync.Mutex
	current context.CancelFunc
	streams sync.WaitGroup
}

// NewServer returns a Server for runtime. authorize decides whether the peer at
// remoteAddr may connect; on the tailnet it accepts only the control plane.
func NewServer(runtime *app.AgentRuntime, authorize func(ctx context.Context, remoteAddr string) error) *Server {
	s := &Server{runtime: runtime, authorize: authorize}
	s.grpc = grpc.NewServer(
		// The control plane pings every 10 s. Without this, gRPC's default
		// policy closes the stream with too_many_pings (ENG-78, result 12).
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 5 * time.Second, PermitWithoutStream: true}),
		grpc.MaxRecvMsgSize(maxMessageBytes),
		grpc.MaxSendMsgSize(maxMessageBytes),
	)
	agentpb.RegisterAgentServer(s.grpc, s)
	return s
}

// Serve accepts connections on listener until Stop. It returns nil after Stop.
func (s *Server) Serve(listener net.Listener) error {
	if err := s.grpc.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return err
	}
	return nil
}

// Stop closes every connection and waits for the stream handlers to return.
func (s *Server) Stop() {
	s.grpc.Stop()
	s.streams.Wait()
}

// Connect serves one stream: State first and on every change, a RunUsage per
// run, and a Reply to every Command.
func (s *Server) Connect(stream agentpb.Agent_ConnectServer) error {
	caller, ok := peer.FromContext(stream.Context())
	if !ok {
		return status.Error(codes.PermissionDenied, "unknown caller")
	}
	if err := s.authorize(stream.Context(), caller.Addr.String()); err != nil {
		return status.Error(codes.PermissionDenied, "only the Janus control plane may connect")
	}
	s.streams.Add(1)
	defer s.streams.Done()
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	s.mu.Lock()
	if s.current != nil {
		s.current()
	}
	s.current = cancel
	s.mu.Unlock()

	var sendMu sync.Mutex
	send := func(event *agentpb.Event) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(event)
	}
	if err := send(stateToProto(s.runtime.State())); err != nil {
		return err
	}
	var handlers sync.WaitGroup
	defer handlers.Wait()
	handlers.Go(func() { s.forward(ctx, send) })

	received := make(chan *agentpb.Command)
	recvErr := make(chan error, 1)
	// The receiver cannot be waited for here: Recv returns only once this
	// handler has returned and gRPC has ended the stream. It exits then, since
	// it never blocks on received after ctx ends, and gRPC owns the stream.
	go func() {
		for {
			command, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			select {
			case received <- command:
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return status.Error(codes.Canceled, "a newer stream replaced this one")
		case err := <-recvErr:
			cancel()
			return err
		case command := <-received:
			// Commands run side by side, so a pause does not wait behind a
			// Deploy's probe. Each Reply names its Command.
			handlers.Go(func() {
				reply := &agentpb.Reply{Id: command.Id}
				if err := s.runtime.Apply(ctx, commandFromProto(command)); err != nil {
					reply.Error = err.Error()
				}
				_ = send(&agentpb.Event{Kind: &agentpb.Event_Reply{Reply: reply}})
			})
		}
	}
}

// forward sends the runtime's reports and state changes until ctx ends.
func (s *Server) forward(ctx context.Context, send func(*agentpb.Event) error) {
	for {
		select {
		case <-ctx.Done():
			return
		case report := <-s.runtime.Reports():
			if err := send(reportToProto(report)); err != nil {
				// The next stream sends it.
				s.runtime.Requeue(report)
				return
			}
		case <-s.runtime.Changes():
			if err := send(stateToProto(s.runtime.State())); err != nil {
				return
			}
		}
	}
}
