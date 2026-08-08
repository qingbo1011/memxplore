// Package telemetry provides the explicit OpenTelemetry OTLP adapter.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/qingbo1011/memxplore/internal/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/qingbo1011/memxplore"

// Config enables OTLP/HTTP only when Endpoint is explicitly non-empty.
type Config struct {
	Endpoint       string
	ServiceName    string
	ServiceVersion string
	httpClient     *http.Client
}

// Runtime owns an optional recorder and its exporter lifecycle.
type Runtime struct {
	Recorder observability.Recorder
	shutdown func(context.Context) error
}

// Shutdown flushes metrics and spans. It is safe for a disabled runtime.
func (r *Runtime) Shutdown(ctx context.Context) error {
	if r == nil || r.shutdown == nil {
		return nil
	}
	return r.shutdown(ctx)
}

// Setup configures global OTel providers for one explicit OTLP/HTTP collector base URL.
func Setup(ctx context.Context, config Config) (*Runtime, error) {
	if strings.TrimSpace(config.Endpoint) == "" {
		return &Runtime{Recorder: observability.Nop()}, nil
	}
	traceURL, metricURL, err := otlpURLs(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if config.ServiceName == "" {
		config.ServiceName = "memxplore"
	}
	traceOptions := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(traceURL), otlptracehttp.WithHeaders(map[string]string{}), otlptracehttp.WithTimeout(5 * time.Second),
	}
	metricOptions := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpointURL(metricURL), otlpmetrichttp.WithHeaders(map[string]string{}), otlpmetrichttp.WithTimeout(5 * time.Second),
	}
	if config.httpClient != nil {
		traceOptions = append(traceOptions, otlptracehttp.WithHTTPClient(config.httpClient))
		metricOptions = append(metricOptions, otlpmetrichttp.WithHTTPClient(config.httpClient))
	}
	traceExporter, err := otlptracehttp.New(ctx, traceOptions...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}
	metricExporter, err := otlpmetrichttp.New(ctx, metricOptions...)
	if err != nil {
		_ = traceExporter.Shutdown(ctx)
		return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
	}
	identity := resource.NewSchemaless(
		attribute.String("service.name", config.ServiceName),
		attribute.String("service.version", config.ServiceVersion),
	)
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(traceExporter), sdktrace.WithResource(identity))
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(identity),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(10*time.Second))),
	)
	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	recorder, err := newOTelRecorder(tracerProvider.Tracer(instrumentationName), meterProvider.Meter(instrumentationName))
	if err != nil {
		_ = meterProvider.Shutdown(ctx)
		_ = tracerProvider.Shutdown(ctx)
		return nil, err
	}
	return &Runtime{
		Recorder: recorder,
		shutdown: func(shutdownContext context.Context) error {
			return errors.Join(meterProvider.Shutdown(shutdownContext), tracerProvider.Shutdown(shutdownContext))
		},
	}, nil
}

type otelRecorder struct {
	tracer     trace.Tracer
	meter      otelmetric.Meter
	operations otelmetric.Int64Counter
	durations  otelmetric.Float64Histogram
	mu         sync.Mutex
	measures   map[string]otelmetric.Float64Histogram
}

func newOTelRecorder(tracer trace.Tracer, meter otelmetric.Meter) (*otelRecorder, error) {
	operations, err := meter.Int64Counter("memxplore.operations", otelmetric.WithDescription("Started MemXplore operations"))
	if err != nil {
		return nil, err
	}
	durations, err := meter.Float64Histogram("memxplore.operation.duration", otelmetric.WithUnit("ms"), otelmetric.WithDescription("MemXplore operation duration"))
	if err != nil {
		return nil, err
	}
	return &otelRecorder{tracer: tracer, meter: meter, operations: operations, durations: durations, measures: make(map[string]otelmetric.Float64Histogram)}, nil
}

func (r *otelRecorder) Start(ctx context.Context, operation string, attrs ...observability.Attribute) (context.Context, observability.EndOperation) {
	started := time.Now()
	base := append([]observability.Attribute(nil), attrs...)
	base = append(base, observability.String("operation", operation))
	otelAttrs := attributes(base)
	ctx, span := r.tracer.Start(ctx, "memxplore."+operation, trace.WithAttributes(otelAttrs...))
	r.operations.Add(ctx, 1, otelmetric.WithAttributes(otelAttrs...))
	return ctx, func(operationErr error, final ...observability.Attribute) {
		outcome := "success"
		if operationErr != nil {
			outcome = "error"
			span.SetStatus(codes.Error, "operation failed")
		}
		completed := append(append([]observability.Attribute(nil), base...), final...)
		completed = append(completed, observability.String("outcome", outcome))
		completedAttrs := attributes(completed)
		span.SetAttributes(completedAttrs...)
		r.durations.Record(ctx, float64(time.Since(started).Microseconds())/1000, otelmetric.WithAttributes(completedAttrs...))
		span.End()
	}
}

func (r *otelRecorder) Observe(ctx context.Context, name string, value float64, attrs ...observability.Attribute) {
	r.mu.Lock()
	histogram := r.measures[name]
	if histogram == nil {
		created, err := r.meter.Float64Histogram(name)
		if err == nil {
			r.measures[name] = created
			histogram = created
		}
	}
	r.mu.Unlock()
	if histogram != nil {
		histogram.Record(ctx, value, otelmetric.WithAttributes(attributes(attrs)...))
	}
}

func attributes(values []observability.Attribute) []attribute.KeyValue {
	result := make([]attribute.KeyValue, 0, len(values))
	for _, value := range values {
		if value.Key != "" && value.Value != "" {
			result = append(result, attribute.String(value.Key, value.Value))
		}
	}
	return result
}

func otlpURLs(endpoint string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", fmt.Errorf("OTLP endpoint must be an http(s) base URL without credentials, query, or fragment")
	}
	basePath := strings.TrimSuffix(parsed.EscapedPath(), "/")
	trace := *parsed
	trace.Path, trace.RawPath = path.Join(basePath, "/v1/traces"), ""
	metric := *parsed
	metric.Path, metric.RawPath = path.Join(basePath, "/v1/metrics"), ""
	return trace.String(), metric.String(), nil
}
