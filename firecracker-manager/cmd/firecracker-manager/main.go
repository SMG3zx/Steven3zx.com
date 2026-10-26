// Command firecracker-manager runs the HTTP API that creates, starts and
// stops Firecracker microVMs, and optionally serves the built web UI.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"firecracker-manager/internal/api"
	"firecracker-manager/internal/vm"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "address to listen on")
	dataDir := flag.String("data-dir", defaultDataDir(), "directory for VM state and per-VM files")
	fcBin := flag.String("firecracker", "firecracker", "path to the firecracker binary")
	webDir := flag.String("web-dir", "", "directory of the built web UI to serve (optional)")
	flag.Parse()

	mgr, err := vm.NewManager(vm.Options{DataDir: *dataDir, FirecrackerBin: *fcBin})
	if err != nil {
		log.Fatalf("firecracker-manager: %v", err)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           api.NewHandler(mgr, *fcBin, *webDir),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		slog.Info("shutting down: stopping running VMs")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("http shutdown", "err", err)
		}
		if err := mgr.StopAll(); err != nil {
			slog.Warn("stopping VMs", "err", err)
		}
	}()

	slog.Info("listening", "addr", *addr, "data_dir", *dataDir)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("firecracker-manager: %v", err)
	}
	<-stopped
}

func defaultDataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "firecracker-manager")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "firecracker-manager-data"
	}
	return filepath.Join(home, ".local", "share", "firecracker-manager")
}
