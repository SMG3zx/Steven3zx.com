// Command janus is the Janus control plane. It is the composition root: the only
// place that knows which adapters exist and how they are wired to the use cases.
//
// `janus` runs the control plane: every service and the gateway in one process.
// `janus serve <service>...` runs only the named services against a NATS server.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	services := serviceNames
	if len(os.Args) > 1 {
		if os.Args[1] != "serve" {
			fmt.Fprintln(os.Stderr, serveUsage)
			os.Exit(2)
		}
		var err error
		if services, err = parseServe(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	logger, err := zap.NewProduction()
	if err != nil {
		return
	}
	logger = logger.WithOptions(zap.WrapCore(func(core zapcore.Core) zapcore.Core {
		return zapcore.NewTee(core, otelzap.NewCore("janus"))
	}))
	defer logger.Sync()

	processErrors := make(chan error, 1)
	application := newServing(logger, processErrors, services)

	startCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	err = application.Start(startCtx)
	cancel()
	if err != nil {
		logger.Error("application startup failed", zap.Error(err))
		return
	}

	select {
	case <-ctx.Done():
	case err := <-processErrors:
		logger.Error("application runtime failure", zap.Error(err))
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := application.Stop(shutdownCtx); err != nil {
		logger.Error("application shutdown failed", zap.Error(err))
	}
}
