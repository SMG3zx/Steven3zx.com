//go:build cgo

package wasmrun

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	wasmtime "github.com/bytecodealliance/wasmtime-go/v49"

	"janus/internal/domain/templates"
)

// guest builds a WASI command from the body of _start. It imports the three raw-net
// functions and fd_write/proc_exit, and has "lo" at 0 and "eth9" at 8.
func guest(t *testing.T, locals, body string) []byte {
	t.Helper()
	wat := `(module
 (import "janus_net_v1" "raw_open" (func $open (param i32 i32) (result i32)))
 (import "janus_net_v1" "raw_recv" (func $recv (param i32 i32 i32 i32) (result i32)))
 (import "janus_net_v1" "raw_close" (func $close (param i32) (result i32)))
 (import "wasi_snapshot_preview1" "fd_write" (func $fd_write (param i32 i32 i32 i32) (result i32)))
 (import "wasi_snapshot_preview1" "proc_exit" (func $exit (param i32)))
 (memory (export "memory") 1)
 (data (i32.const 0) "lo")
 (data (i32.const 8) "eth9")
 (func (export "_start") ` + locals + body + `))`
	module, err := wasmtime.Wat2Wasm(wat)
	if err != nil {
		t.Fatalf("assemble the guest: %v", err)
	}
	return module
}

// captureOne opens "lo", waits for a frame and writes it to stdout.
const captureOne = `
   (local.set $h (call $open (i32.const 0) (i32.const 2)))
   (if (i32.lt_s (local.get $h) (i32.const 0)) (then (call $exit (i32.sub (i32.const 0) (local.get $h)))))
   (loop $again
     (local.set $n (call $recv (local.get $h) (i32.const 64) (i32.const 1024) (i32.const 200)))
     (br_if $again (i32.eqz (local.get $n))))
   (if (i32.lt_s (local.get $n) (i32.const 0)) (then (call $exit (i32.sub (i32.const 0) (local.get $n)))))
   (i32.store (i32.const 16) (i32.const 64))
   (i32.store (i32.const 20) (local.get $n))
   (drop (call $fd_write (i32.const 1) (i32.const 16) (i32.const 1) (i32.const 24)))
   (drop (call $close (local.get $h)))`

const handleLocals = `(local $h i32) (local $n i32) (local $i i32)`

type fakeSource struct {
	frames [][]byte
	mu     sync.Mutex
	closed *atomic.Int32
}

func (f *fakeSource) Recv(buf []byte, timeout time.Duration) (int, error) {
	f.mu.Lock()
	if len(f.frames) > 0 {
		frame := f.frames[0]
		f.frames = f.frames[1:]
		f.mu.Unlock()
		return copy(buf, frame), nil
	}
	f.mu.Unlock()
	time.Sleep(timeout)
	return 0, nil
}

func (f *fakeSource) Close() error { f.closed.Add(1); return nil }

type audited struct {
	mu     sync.Mutex
	events []string
	attrs  []map[string]string
}

func (a *audited) record(event string, attrs map[string]string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, event)
	a.attrs = append(a.attrs, attrs)
}

func limits(timeout time.Duration) templates.Resources {
	return templates.Resources{Fuel: fuelLimit, MemoryBytes: templates.DefaultMemoryBytes, Timeout: timeout}
}

func fakeOpener(closed *atomic.Int32, frames ...[]byte) PacketOpener {
	return func(string) (PacketSource, error) { return &fakeSource{frames: frames, closed: closed}, nil }
}

func rawGrant(interfaces ...string) []Grant {
	return []Grant{{Capability: CapabilityRawNet, Interfaces: interfaces}}
}

func TestAModuleThatImportsAnUngrantedCapabilityDoesNotStart(t *testing.T) {
	_, err := Run(context.Background(), guest(t, handleLocals, captureOne), Input{Limits: limits(5 * time.Second)})
	if !errors.Is(err, ErrCapabilityNotGranted) {
		t.Fatalf("err = %v, want ErrCapabilityNotGranted", err)
	}
	var capability *CapabilityError
	if !errors.As(err, &capability) || capability.Capability != CapabilityRawNet {
		t.Fatalf("err = %v, want a CapabilityError naming raw-net", err)
	}
}

func TestAGrantedModuleReceivesAFrameFromAnAllowedInterface(t *testing.T) {
	var closed atomic.Int32
	var log audited
	result, err := Run(context.Background(), guest(t, handleLocals, captureOne), Input{
		Limits: limits(5 * time.Second), Grants: rawGrant("lo"), Audit: log.record,
		RawNetOpener: fakeOpener(&closed, []byte("frame-one")),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(result.Stdout) != "frame-one" {
		t.Fatalf("stdout = %q, want the frame", result.Stdout)
	}
	if closed.Load() != 1 {
		t.Fatalf("closed = %d, want 1", closed.Load())
	}
	if strings.Join(log.events, ",") != "raw_net.open,raw_net.close" || log.attrs[1]["frames"] != "1" || log.attrs[1]["bytes"] != "9" {
		t.Fatalf("audit = %v %v, want an open and a close with the counts", log.events, log.attrs)
	}
}

func TestAnInterfaceOutsideTheGrantIsRefused(t *testing.T) {
	var closed atomic.Int32
	var log audited
	// captureOne opens "lo"; the grant allows only "eth0", so raw_open returns -2.
	_, err := Run(context.Background(), guest(t, handleLocals, captureOne), Input{
		Limits: limits(5 * time.Second), Grants: rawGrant("eth0"), Audit: log.record,
		RawNetOpener: fakeOpener(&closed),
	})
	if err == nil || !strings.Contains(err.Error(), "status 2") {
		t.Fatalf("err = %v, want the guest to exit with status 2 (not allowed)", err)
	}
	if closed.Load() != 0 || len(log.attrs) != 1 || log.attrs[0]["result"] != "denied" {
		t.Fatalf("closed = %d, audit = %v, want one denied open and nothing opened", closed.Load(), log.attrs)
	}
}

func TestOpenCapturesAreLimitedAndClosedAfterTheRun(t *testing.T) {
	var closed atomic.Int32
	// Open "lo" five times and leave them all open, exiting with the last code negated.
	body := `
   (loop $again
     (local.set $h (call $open (i32.const 0) (i32.const 2)))
     (local.set $i (i32.add (local.get $i) (i32.const 1)))
     (br_if $again (i32.and (i32.gt_s (local.get $h) (i32.const 0)) (i32.lt_s (local.get $i) (i32.const 5)))))
   (call $exit (i32.sub (i32.const 0) (local.get $h)))`
	_, err := Run(context.Background(), guest(t, handleLocals, body), Input{
		Limits: limits(5 * time.Second), Grants: rawGrant("lo"), RawNetOpener: fakeOpener(&closed),
	})
	if err == nil || !strings.Contains(err.Error(), "status 4") {
		t.Fatalf("err = %v, want the fifth open refused with status 4 (too many)", err)
	}
	if closed.Load() != maxRawHandles {
		t.Fatalf("closed = %d, want the %d captures the module left open closed after the run", closed.Load(), maxRawHandles)
	}
}

func TestAGuestPointerOutsideItsMemoryIsRefusedNotTrusted(t *testing.T) {
	var closed atomic.Int32
	body := `
   (local.set $h (call $open (i32.const 0) (i32.const 2)))
   (local.set $n (call $recv (local.get $h) (i32.const -16) (i32.const 100) (i32.const 10)))
   (call $exit (i32.sub (i32.const 0) (local.get $n)))`
	_, err := Run(context.Background(), guest(t, handleLocals, body), Input{
		Limits: limits(5 * time.Second), Grants: rawGrant("lo"), RawNetOpener: fakeOpener(&closed, []byte("x")),
	})
	if err == nil || !strings.Contains(err.Error(), "status 1") {
		t.Fatalf("err = %v, want a bad-arguments refusal (status 1)", err)
	}
}

func TestAnUnknownHandleIsRefused(t *testing.T) {
	body := `(call $exit (i32.sub (i32.const 0) (call $recv (i32.const 99) (i32.const 64) (i32.const 10) (i32.const 10))))`
	_, err := Run(context.Background(), guest(t, "", body), Input{Limits: limits(5 * time.Second), Grants: rawGrant("lo")})
	if err == nil || !strings.Contains(err.Error(), "status 6") {
		t.Fatalf("err = %v, want a bad-handle refusal (status 6)", err)
	}
}

func TestAGuestWaitingForAFrameStillHitsTheRunTimeout(t *testing.T) {
	var closed atomic.Int32
	// Wait a minute for a frame that never comes, under a one second run limit.
	body := `
   (local.set $h (call $open (i32.const 0) (i32.const 2)))
   (drop (call $recv (local.get $h) (i32.const 64) (i32.const 1024) (i32.const 60000)))`
	started := time.Now()
	_, err := Run(context.Background(), guest(t, handleLocals, body), Input{
		Limits: limits(time.Second), Grants: rawGrant("lo"), RawNetOpener: fakeOpener(&closed),
	})
	elapsed := time.Since(started)
	if !errors.Is(err, ErrRunTimeout) {
		t.Fatalf("err = %v, want ErrRunTimeout", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("the run took %v, want it to end near its 1s limit", elapsed)
	}
	if closed.Load() != 1 {
		t.Fatalf("closed = %d, want the capture closed", closed.Load())
	}
}

func TestACancelledRunEndsAGuestWaitingForAFrame(t *testing.T) {
	var closed atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	body := `
   (local.set $h (call $open (i32.const 0) (i32.const 2)))
   (drop (call $recv (local.get $h) (i32.const 64) (i32.const 1024) (i32.const 60000)))`
	started := time.Now()
	_, err := Run(ctx, guest(t, handleLocals, body), Input{
		Limits: limits(time.Minute), Grants: rawGrant("lo"), RawNetOpener: fakeOpener(&closed),
	})
	if err == nil || time.Since(started) > 3*time.Second {
		t.Fatalf("err = %v after %v, want the cancelled run to stop promptly", err, time.Since(started))
	}
}
