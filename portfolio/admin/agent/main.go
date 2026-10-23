// Command hostagent runs natively on the Windows origin host and serves a
// JSON snapshot of the machine (CPU, memory, network, storage, processes,
// services, Docker) to the admin container over host.docker.internal.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"
)

const tokenKey = "HOST_AGENT_TOKEN"

func main() {
	addr := flag.String("addr", workerAddr, "listen address")
	envFile := flag.String("env", "", "optional .env file to read "+tokenKey+" from")
	pipe := flag.String("docker-pipe", `\\.\pipe\docker_engine`, "Docker engine named pipe")
	supervise := flag.Bool("supervise", false, "run the supervisor: keep the agent running as a child and serve restart requests on "+controlAddr)
	flag.Parse()

	token := os.Getenv(tokenKey)
	if token == "" && *envFile != "" {
		var err error
		if token, err = readEnvValue(*envFile, tokenKey); err != nil {
			log.Fatalf("reading %s: %v", *envFile, err)
		}
	}
	if len(token) < 32 {
		log.Fatalf("%s must be set to at least 32 characters", tokenKey)
	}

	if *supervise {
		exe, err := os.Executable()
		if err != nil {
			log.Fatalf("locating hostagent: %v", err)
		}
		run := func(ctx context.Context) error { return runSupervisor(ctx, exe, *envFile, token) }
		if handled, err := runIfService(run); handled {
			if err != nil {
				log.Fatal(err)
			}
			return
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if err := run(ctx); err != nil {
			log.Fatal(err)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	col := newCollector(newDockerClient(*pipe))
	go col.Run(ctx)

	authorized := func(r *http.Request) bool { return bearerOK(r, token) }

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	// GET /snapshot returns everything. GET /snapshot?topics=cpu,memory returns
	// only those parts, which is what the admin server asks for on each frame.
	mux.HandleFunc("GET /snapshot", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		snap := col.Snapshot()
		var body any = snap
		if raw := r.URL.Query().Get("topics"); raw != "" {
			body = snap.project(strings.Split(raw, ","))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(body)
	})
	// GET /logs/{id} streams a container's log as NDJSON, starting with the
	// last lines and then following new output until the client disconnects.
	mux.HandleFunc("GET /logs/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id := r.PathValue("id")
		if !containerIDPattern.MatchString(id) {
			http.Error(w, "invalid container id", http.StatusBadRequest)
			return
		}
		body, err := col.docker.openLogs(r.Context(), id)
		if errors.Is(err, errNoContainer) {
			http.Error(w, "no such container", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Printf("container logs %s: %v", id, err)
			http.Error(w, "docker unavailable", http.StatusBadGateway)
			return
		}
		defer body.Close()
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-store")
		rc := http.NewResponseController(w)
		enc := json.NewEncoder(w)
		err = readDockerLogs(body, func(l logLine) error {
			if err := enc.Encode(l); err != nil {
				return err
			}
			return rc.Flush()
		})
		if err != nil && r.Context().Err() == nil {
			log.Printf("container logs %s: %v", id, err)
		}
	})

	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Printf("host agent listening on %s", *addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func readEnvValue(path, key string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if ok && strings.TrimSpace(k) == key {
			return strings.Trim(strings.TrimSpace(v), `"'`), nil
		}
	}
	return "", sc.Err()
}
