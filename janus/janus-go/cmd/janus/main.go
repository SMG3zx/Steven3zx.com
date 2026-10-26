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
	// Spawn a listener for the "close the application signal"
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The platform only supports the "serve" command
	// So if it is not that, we print the usage
	// Then if we get the "serve" command we call parseServe with what comes after serve
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

	// Start the structured logging
	logger, err := zap.NewProduction()
	if err != nil {
		return
	}
	logger = logger.WithOptions(zap.WrapCore(func(core zapcore.Core) zapcore.Core {
		return zapcore.NewTee(core, otelzap.NewCore("janus"))
	}))
	defer logger.Sync()

	// We spawn a channel to listen for any runtime failures from serving the application
	processErrors := make(chan error, 1)
	// We then create the application instance
	application := newServing(logger, processErrors, services)

	// We then start the application with a startup timeout
	startCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	err = application.Start(startCtx)

	// we call cancel to release the resources associated with starting the application
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
