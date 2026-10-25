package ingest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"

	"fleetops/internal/testinfra"
)

// loopEnv is one migrated database, one JetStream, and one consumer wiring.
// The clock is time.Now: the consumer's interval trigger and staleness both need real time.
type loopEnv struct {
	t       testing.TB
	fe      *flushEnv
	js      jetstream.JetStream
	gate    *Gate
	buf     *Buffer
	m       *Metrics
	reg     *prometheus.Registry
	cons    *Consumer
	vehicle int64
	device  int64
}

// newLoopEnv builds the wiring with the given consumer spec and flush max.
// The interval is the GF-80 default of one second.
func newLoopEnv(t testing.TB, spec consumerSpec, flushMax int) *loopEnv {
	t.Helper()
	ctx := context.Background()
	fe := newFlushEnv(t)
	if err := EnsurePartitions(ctx, fe.conn, time.Now()); err != nil {
		t.Fatalf("EnsurePartitions: %v", err)
	}

	js := testinfra.NewJetStream(t)
	if err := EnsureStream(ctx, js); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}
	if _, err := ensureConsumer(ctx, js, spec); err != nil {
		t.Fatalf("ensureConsumer: %v", err)
	}

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	buf := NewBuffer(time.Second, flushMax, 50, fe.counters)
	cons := NewConsumer(js, buf, fe.writer, m, time.Now)
	cons.spec = spec

	vehicleID, deviceID := fe.pairedVehicle(0)
	return &loopEnv{t: t, fe: fe, js: js, gate: NewGate(), buf: buf, m: m, reg: reg, cons: cons, vehicle: vehicleID, device: deviceID}
}

// liveMsg is a paired, fresh, accurate message stamped at serverTS.
func (l *loopEnv) liveMsg(serverTS time.Time) Message {
	m := l.fe.msg(l.device, serverTS, 41.0, 29.0, 10)
	m.Position.Accuracy = 5
	return m
}

// publish sends m through the production gateway publish path (GF-60).
func (l *loopEnv) publish(m Message) {
	l.t.Helper()
	if err := l.gate.Publish(l.js, m); err != nil {
		l.t.Fatalf("publish: %v", err)
	}
}

// start runs the consumer in a goroutine. The returned stop cancels its context
// and waits for Run to return; it is safe to call more than once.
func (l *loopEnv) start() (stop func() error) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- l.cons.Run(ctx) }()
	var once sync.Once
	var runErr error
	stop = func() error {
		once.Do(func() {
			cancel()
			runErr = <-done
		})
		return runErr
	}
	l.t.Cleanup(func() { _ = stop() })
	return stop
}

// rows counts the positions rows.
func (l *loopEnv) rows() int {
	return l.fe.count(`SELECT count(*) FROM positions`)
}

// waitRows polls until positions holds want rows, or fails after 15 s.
func (l *loopEnv) waitRows(want int) {
	l.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for l.rows() != want {
		if time.Now().After(deadline) {
			l.t.Fatalf("positions has %d rows after 15s, want %d", l.rows(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitAckPending polls the durable consumer until NumAckPending is 0, or fails after 15 s.
func (l *loopEnv) waitAckPending() {
	l.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, err := l.js.Stream(ctx, StreamName)
	if err != nil {
		l.t.Fatalf("stream: %v", err)
	}
	cons, err := s.Consumer(ctx, defaultConsumerSpec().durable)
	if err != nil {
		l.t.Fatalf("consumer: %v", err)
	}
	for {
		info, err := cons.Info(ctx)
		if err != nil {
			l.t.Fatalf("consumer info: %v", err)
		}
		if info.NumAckPending == 0 {
			return
		}
		if ctx.Err() != nil {
			l.t.Fatalf("NumAckPending = %d after 15s, want 0", info.NumAckPending)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitCounter polls a labelled counter until it reaches want, or fails after 15 s.
func (l *loopEnv) waitCounter(name, label, value string, want float64) {
	l.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for counterValue(l.t, l.reg, name, label, value) < want {
		if time.Now().After(deadline) {
			l.t.Fatalf("%s{%s=%q} = %v after 15s, want %v", name, label, value, counterValue(l.t, l.reg, name, label, value), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// counterValue reads one counter from reg. label and value select the series;
// an empty label reads the single unlabelled series.
func counterValue(t testing.TB, reg *prometheus.Registry, name, label, value string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
		for _, metric := range mf.GetMetric() {
			if label == "" {
				return metric.GetCounter().GetValue()
			}
			for _, lp := range metric.GetLabel() {
				if lp.GetName() == label && lp.GetValue() == value {
					return metric.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}
