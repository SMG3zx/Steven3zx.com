package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	goakt "github.com/tochemey/goakt/v4/actor"
	golog "github.com/tochemey/goakt/v4/log"
	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"janus/actors/application"
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

	system, err := goakt.NewActorSystem("janus", goakt.WithLogger(golog.NewZapFrom(logger)))
	if err != nil {
		logger.Fatal("create actor system", zap.Error(err))
	}
	if err := system.Start(ctx); err != nil {
		logger.Fatal("start actor system", zap.Error(err))
	}

	app, err := system.Spawn(ctx, "application", application.New(logger))
	if err != nil {
		logger.Fatal("spawn application actor", zap.Error(err))
	}

	applicationErrors := make(chan error, 1)
	started, err := goakt.Ask(ctx, app, &application.Start{Errors: applicationErrors}, application.StartupTimeout)
	if err != nil {
		logger.Fatal("start application", zap.Error(err))
	}
	startResult, ok := started.(*application.Started)
	if !ok {
		logger.Fatal("start application: unexpected response", zap.String("response_type", fmt.Sprintf("%T", started)))
	}
	if startResult.Err != nil {
		logger.Error("application startup failed", zap.Error(startResult.Err))
		stopSystem(system, logger)
		return
	}

	select {
	case <-ctx.Done():
	case err := <-applicationErrors:
		logger.Error("application runtime failure", zap.Error(err))
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), application.ShutdownTimeout)
	defer cancel()
	stopped, err := goakt.Ask(shutdownCtx, app, &application.Stop{Context: shutdownCtx}, application.ShutdownTimeout)
	if err != nil {
		logger.Error("stop application", zap.Error(err))
	} else if stopResult, ok := stopped.(*application.Stopped); !ok {
		logger.Error("stop application: unexpected response", zap.String("response_type", fmt.Sprintf("%T", stopped)))
	} else if stopResult.Err != nil {
		logger.Error("application shutdown failed", zap.Error(stopResult.Err))
	}
	if err := system.Stop(shutdownCtx); err != nil {
		logger.Error("stop actor system", zap.Error(err))
	}
}

func stopSystem(system goakt.ActorSystem, logger *zap.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), application.ShutdownTimeout)
	defer cancel()
	if err := system.Stop(ctx); err != nil {
		logger.Error("stop actor system", zap.Error(err))
	}
}
