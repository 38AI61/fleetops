package observability

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// endpointEnv is the standard OTLP endpoint variable. Unset or empty means no-op (D-36, GF-334).
const endpointEnv = "OTEL_EXPORTER_OTLP_ENDPOINT"

// InitTracing installs an OTLP HTTP tracer provider when OTEL_EXPORTER_OTLP_ENDPOINT is set.
// Otherwise it sets nothing: the global provider stays the no-op default, and the returned
// shutdown does nothing. lookup is os.LookupEnv in production. The returned shutdown is never nil.
func InitTracing(ctx context.Context, lookup func(string) (string, bool), service string) (func(context.Context) error, error) {
	if endpoint, _ := lookup(endpointEnv); endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	// otlptracehttp reads the standard OTEL_* endpoint variables itself.
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", service))),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}
