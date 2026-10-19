// Command hostagent runs natively on the Windows origin host and serves a
// JSON snapshot of the machine (CPU, memory, network, storage, processes,
// services, Docker) to the admin container over host.docker.internal.
package main

import (
	"bufio"
	"context"
	"crypto/subtle"
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
	addr := flag.String("addr", "127.0.0.1:9180", "listen address")
	envFile := flag.String("env", "", "optional .env file to read "+tokenKey+" from")
	pipe := flag.String("docker-pipe", `\\.\pipe\docker_engine`, "Docker engine named pipe")
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	col := newCollector(newDockerClient(*pipe))
	go col.Run(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /snapshot", func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(col.Snapshot())
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
