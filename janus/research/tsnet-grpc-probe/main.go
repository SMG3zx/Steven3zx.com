// Command tsgrpc tests a gRPC bidirectional stream between a control-plane
// tsnet node (which dials) and an Agent tsnet node (which listens).
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"tailscale.com/tsnet"
)

var name string

func logf(format string, args ...any) {
	fmt.Printf("[%s] %s %s\n", name, time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

func main() {
	role := flag.String("role", "", "agent|ctrl")
	flag.StringVar(&name, "name", "", "hostname")
	keyFile := flag.String("keyfile", "", "pre-auth key file")
	dir := flag.String("dir", "", "state dir")
	control := flag.String("control", "http://127.0.0.1:8080", "control url")
	target := flag.String("target", "agent-a:9100", "agent address (ctrl)")
	cmdEvery := flag.Duration("cmd", 3*time.Second, "ctrl sends a command this often; 0 = never")
	eventEvery := flag.Duration("event", 5*time.Second, "agent sends an event this often; 0 = never")
	hold := flag.Duration("hold", 120*time.Second, "how long to run")
	kaTime := flag.Duration("katime", 10*time.Second, "keepalive ping interval")
	kaTimeout := flag.Duration("katimeout", 5*time.Second, "keepalive ping timeout")
	permit := flag.Bool("permitpings", false, "agent permits frequent client pings")
	flag.Parse()

	key, _ := os.ReadFile(*keyFile)
	srv := &tsnet.Server{Dir: *dir, Hostname: name, AuthKey: strings.TrimSpace(string(key)), ControlURL: *control, Logf: func(string, ...any) {}, UserLogf: func(string, ...any) {}}
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	if _, err := srv.Up(ctx); err != nil {
		logf("UP FAILED: %v", err)
		os.Exit(1)
	}
	cancel()
	logf("UP")
	end := time.Now().Add(*hold)

	if *role == "agent" {
		runAgent(srv, *eventEvery, *permit)
		time.Sleep(time.Until(end))
		return
	}
	runCtrl(srv, *target, *cmdEvery, *kaTime, *kaTimeout, end)
}

func runAgent(srv *tsnet.Server, eventEvery time.Duration, permit bool) {
	ln, err := srv.Listen("tcp", ":9100")
	if err != nil {
		logf("listen: %v", err)
		os.Exit(1)
	}
	opts := []grpc.ServerOption{}
	if permit {
		opts = append(opts, grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 5 * time.Second, PermitWithoutStream: true}))
	}
	gs := grpc.NewServer(opts...)
	handler := func(_ any, stream grpc.ServerStream) error {
		logf("AGENT stream opened")
		var mu sync.Mutex
		send := func(s string) error {
			mu.Lock()
			defer mu.Unlock()
			return stream.SendMsg(wrapperspb.String(s))
		}
		if eventEvery > 0 {
			go func() {
				t := time.NewTicker(eventEvery)
				defer t.Stop()
				for i := 1; ; i++ {
					select {
					case <-stream.Context().Done():
						return
					case <-t.C:
						if err := send(fmt.Sprintf("event %d", i)); err != nil {
							return
						}
					}
				}
			}()
		}
		for {
			m := new(wrapperspb.StringValue)
			if err := stream.RecvMsg(m); err != nil {
				logf("AGENT stream ended: %v", err)
				return err
			}
			logf("AGENT got %q", m.Value)
			if err := send("ack " + m.Value); err != nil {
				return err
			}
		}
	}
	gs.RegisterService(&grpc.ServiceDesc{
		ServiceName: "janus.Agent",
		HandlerType: (*any)(nil),
		Streams:     []grpc.StreamDesc{{StreamName: "Connect", Handler: handler, ServerStreams: true, ClientStreams: true}},
	}, struct{}{})
	go gs.Serve(ln)
}

func runCtrl(srv *tsnet.Server, target string, cmdEvery, kaTime, kaTimeout time.Duration, end time.Time) {
	conn, err := grpc.NewClient("passthrough:///"+target,
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) { return srv.Dial(ctx, "tcp", addr) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: kaTime, Timeout: kaTimeout, PermitWithoutStream: true}))
	if err != nil {
		logf("client: %v", err)
		os.Exit(1)
	}
	defer conn.Close()
	desc := &grpc.StreamDesc{StreamName: "Connect", ServerStreams: true, ClientStreams: true}
	for time.Now().Before(end) {
		attempt := time.Now()
		sctx, cancel := context.WithCancel(context.Background())
		stream, err := conn.NewStream(sctx, desc, "/janus.Agent/Connect", grpc.WaitForReady(true))
		if err != nil {
			logf("CTRL NewStream failed: %v", err)
			cancel()
			time.Sleep(time.Second)
			continue
		}
		logf("CTRL stream UP (waited %s)", time.Since(attempt).Round(time.Millisecond))
		down := make(chan error, 1)
		go func() {
			for {
				m := new(wrapperspb.StringValue)
				if err := stream.RecvMsg(m); err != nil {
					down <- err
					return
				}
				logf("CTRL got %q", m.Value)
			}
		}()
		var tick <-chan time.Time
		if cmdEvery > 0 {
			t := time.NewTicker(cmdEvery)
			defer t.Stop()
			tick = t.C
		}
	loop:
		for i := 1; ; i++ {
			select {
			case err := <-down:
				logf("CTRL stream DOWN: %v", err)
				break loop
			case <-tick:
				if err := stream.SendMsg(wrapperspb.String(fmt.Sprintf("cmd %d", i))); err != nil {
					logf("CTRL send failed: %v", err)
				}
			case <-time.After(time.Until(end)):
				cancel()
				return
			}
		}
		cancel()
		time.Sleep(time.Second)
	}
}
