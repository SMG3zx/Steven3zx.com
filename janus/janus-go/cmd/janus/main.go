// Command janus is the Janus control plane. It is the composition root: the only
// place that knows which adapters exist and how they are wired to the use cases.
//
// `janus` runs the control plane.
package main

import (
	"context"
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
	logger, err := zap.NewProduction()
	if err != nil {
		return
	}
	logger = logger.WithOptions(zap.WrapCore(func(core zapcore.Core) zapcore.Core {
		return zapcore.NewTee(core, otelzap.NewCore("janus"))
	}))
	defer logger.Sync()

	processErrors := make(chan error, 1)
	application := newApplication(logger, processErrors)

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
