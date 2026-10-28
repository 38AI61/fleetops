package main

import (
	"fmt"
	"net/http"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// tracedHarness is startServer with an in-memory span exporter and no collector (GF-335).
func tracedHarness(t *testing.T) (*harness, *tracetest.InMemoryExporter) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(t.Context()) })
	return startServerOpts(t, nil, tp.Tracer("fleetops/jobs")), exp
}

// assignSpans returns the jobs.assign spans recorded so far.
func assignSpans(exp *tracetest.InMemoryExporter) []tracetest.SpanStub {
	var out []tracetest.SpanStub
	for _, s := range exp.GetSpans() {
		if s.Name == "jobs.assign" {
			out = append(out, s)
		}
	}
	return out
}

// jobIDAttr reads the job.id attribute of a span.
func jobIDAttr(t *testing.T, s tracetest.SpanStub) int64 {
	t.Helper()
	for _, kv := range s.Attributes {
		if kv.Key == attribute.Key("job.id") {
			return kv.Value.AsInt64()
		}
	}
	t.Fatalf("span %s has no job.id attribute", s.Name)
	return 0
}

// TestAssignEmitsSpan checks one applied assign emits one jobs.assign span carrying job.id, with no error.
func TestAssignEmitsSpan(t *testing.T) {
	h, exp := tracedHarness(t)
	f := newJobsFleet(t, h, "span-ok")
	v, d := f.vehicle(t, h, "SP-1"), f.driver(t, h, "sp-driver")
	job := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)

	assignJob(t, h, f.dispTok, job, v, d)

	spans := assignSpans(exp)
	if len(spans) != 1 {
		t.Fatalf("jobs.assign spans = %d, want 1", len(spans))
	}
	if got := jobIDAttr(t, spans[0]); got != job {
		t.Fatalf("job.id = %d, want %d", got, job)
	}
	if spans[0].Status.Code == codes.Error {
		t.Fatalf("span status = Error on an applied assign: %s", spans[0].Status.Description)
	}
}

// TestAssignSpanOnError checks a refused assign (a cancelled job, 409) records an error status
// on the span and leaves the response unchanged.
func TestAssignSpanOnError(t *testing.T) {
	h, exp := tracedHarness(t)
	f := newJobsFleet(t, h, "span-err")
	v, d := f.vehicle(t, h, "SE-1"), f.driver(t, h, "se-driver")
	job := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)
	if r := h.do(t, http.MethodPost, fmt.Sprintf("/jobs/%d/cancel", job), f.dispTok, "{}", nil); r.status != http.StatusOK {
		t.Fatalf("cancel = %d %s", r.status, r.body)
	}

	r := h.do(t, http.MethodPost, fmt.Sprintf("/jobs/%d/assign", job), f.dispTok,
		fmt.Sprintf(`{"vehicle_id":%d,"driver_id":%d}`, v, d), nil)
	if r.status != http.StatusConflict {
		t.Fatalf("assign cancelled job = %d %s, want 409", r.status, r.body)
	}

	spans := assignSpans(exp)
	if len(spans) != 1 {
		t.Fatalf("jobs.assign spans = %d, want 1", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Fatalf("span status = %v, want Error", spans[0].Status.Code)
	}
	if got := jobIDAttr(t, spans[0]); got != job {
		t.Fatalf("job.id = %d, want %d", got, job)
	}
}

// TestAssignReplayStillSpans checks a replayed assign (same vehicle and driver) still emits one span.
func TestAssignReplayStillSpans(t *testing.T) {
	h, exp := tracedHarness(t)
	f := newJobsFleet(t, h, "span-replay")
	v, d := f.vehicle(t, h, "SR-1"), f.driver(t, h, "sr-driver")
	job := createJob(t, h, f.dispTok, `{`+jobPoints+`}`)
	assignJob(t, h, f.dispTok, job, v, d)
	before := len(assignSpans(exp))

	assignJob(t, h, f.dispTok, job, v, d)

	if after := len(assignSpans(exp)); after != before+1 {
		t.Fatalf("replay spans = %d, want %d", after, before+1)
	}
}
