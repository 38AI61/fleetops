package observability_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"fleetops/internal/observability"
)

// TestInitTracingUnsetIsNoop checks that an unset or empty OTEL_EXPORTER_OTLP_ENDPOINT installs no
// SDK provider, returns no error, and gives a nil-safe shutdown (D-36, GF-334).
func TestInitTracingUnsetIsNoop(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(string) (string, bool){
		"unset": func(string) (string, bool) { return "", false },
		"empty": func(string) (string, bool) { return "", true },
	}
	for name, lookup := range cases {
		t.Run(name, func(t *testing.T) {
			shutdown, err := observability.InitTracing(ctx, lookup, "fleetops-api")
			if err != nil {
				t.Fatalf("InitTracing: %v", err)
			}
			if shutdown == nil {
				t.Fatal("shutdown is nil, want a no-op")
			}
			if _, isSDK := otel.GetTracerProvider().(*sdktrace.TracerProvider); isSDK {
				t.Fatal("global tracer provider is an SDK provider, want the default no-op")
			}
			if err := shutdown(ctx); err != nil {
				t.Fatalf("shutdown: %v", err)
			}
		})
	}
}
