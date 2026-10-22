// Command indexd is the spotlight daemon: it indexes configured folders, keeps
// the index current via fsnotify, and serves search on localhost.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"spotlight/internal/config"
	"spotlight/internal/embed"
	"spotlight/internal/indexer"
	"spotlight/internal/search"
	"spotlight/internal/server"
	"spotlight/internal/store"
	"spotlight/internal/throttle"
	"spotlight/internal/watch"
)

func main() {
	cfgPath := flag.String("config", "config.json", "path to the JSON config file")
	once := flag.Bool("once", false, "run one full index pass and exit")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(*cfgPath, *once, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(cfgPath string, once bool, log *slog.Logger) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	gov := throttle.New(throttle.Limits{
		CPU: cfg.MaxCPUPercent,
		Mem: cfg.MaxMemPercent,
		GPU: cfg.MaxGPUPercent,
	}, log)
	gov.Probe(ctx)

	emb := embed.NewOllama(cfg.OllamaURL, cfg.EmbedModel)
	ix := indexer.New(st, emb, gov, cfg, log)

	if once {
		n, err := ix.Scan(ctx)
		log.Info("scan complete", "files", n)
		return err
	}

	go func() {
		n, err := ix.Scan(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Error("initial scan failed", "err", err)
			}
			return
		}
		log.Info("initial scan complete", "files", n)
	}()

	if cfg.Watch {
		go func() {
			err := watch.Run(ctx, cfg.Roots, cfg.IgnoreDirs, func(c context.Context, p string) {
				if err := ix.IndexFile(c, p); err != nil {
					log.Warn("index failed", "path", p, "err", err)
				}
			}, log)
			if err != nil {
				log.Error("watcher stopped", "err", err)
			}
		}()
	}

	if cfg.RescanMinutes > 0 {
		go rescanLoop(ctx, ix, time.Duration(cfg.RescanMinutes)*time.Minute, log)
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.New(st, search.New(st, emb, log), log).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("listening", "addr", cfg.ListenAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// rescanLoop backs up the watcher: it catches events fsnotify misses, such as
// deleted directories and changes made while the daemon was not running.
func rescanLoop(ctx context.Context, ix *indexer.Indexer, every time.Duration, log *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := ix.Scan(ctx); err != nil && ctx.Err() == nil {
				log.Warn("rescan failed", "err", err)
			}
		}
	}
}
