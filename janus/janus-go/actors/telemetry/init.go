package telemetry

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

var (
	requestCount    metric.Int64Counter
	requestDuration metric.Int64Histogram
)

// Setup installs a global tracer provider and returns a shutdown func that flushes pending spans.
func Init(ctx context.Context, service string) (func(context.Context) error, error) {
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(attribute.String("service.name", service)),
	)

	if err != nil {
		return nil, err
	}

	metricExporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		_ = exp.Shutdown(ctx)
		return nil, err
	}
	logExporter, err := otlploghttp.New(ctx)
	if err != nil {
		_ = metricExporter.Shutdown(ctx)
		_ = exp.Shutdown(ctx)
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(15*time.Second))),
		sdkmetric.WithResource(res),
	)
	lp := log.NewLoggerProvider(log.WithProcessor(log.NewBatchProcessor(logExporter, log.WithExportInterval(2*time.Second))), log.WithResource(res))
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	otel.SetLoggerProvider(lp)
	requestCount, err = otel.Meter("janus/actors/telemetry").Int64Counter("http.server.request.count")
	if err != nil {
		_ = lp.Shutdown(ctx)
		_ = mp.Shutdown(ctx)
		_ = tp.Shutdown(ctx)
		return nil, err
	}
	requestDuration, err = otel.Meter("janus/actors/telemetry").Int64Histogram("http.server.request.duration", metric.WithUnit("ms"))
	if err != nil {
		_ = lp.Shutdown(ctx)
		_ = mp.Shutdown(ctx)
		_ = tp.Shutdown(ctx)
		return nil, err
	}

	// Lets incoming requests continue a trace started by an upstream service.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return func(shutdownCtx context.Context) error {
		return errors.Join(lp.Shutdown(shutdownCtx), mp.Shutdown(shutdownCtx), tp.Shutdown(shutdownCtx))
	}, nil
}

// RecordHTTPRequest records the method, response status, and elapsed time of an
// HTTP request in the configured telemetry providers.
func RecordHTTPRequest(ctx context.Context, method string, status int, duration time.Duration) {
	if requestCount == nil || requestDuration == nil {
		return
	}
	attributes := metric.WithAttributes(attribute.String("http.request.method", method), attribute.Int("http.response.status_code", status))
	requestCount.Add(ctx, 1, attributes)
	requestDuration.Record(ctx, duration.Milliseconds(), attributes)
}
