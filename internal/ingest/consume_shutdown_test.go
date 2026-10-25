package ingest

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// runWithCancel runs the consumer under its own cancel. The returned channel
// yields Run's error once Run has returned, so the test may then touch seams.
func (l *loopEnv) runWithCancel(ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() { done <- l.cons.Run(ctx) }()
	return done
}

// fetchRaw pulls k messages from the durable consumer outside the loop, so the
// test can read their delivery metadata. The caller decides their ack state.
func (l *loopEnv) fetchRaw(k int) []jetstream.Msg {
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
	batch, err := cons.Fetch(k, jetstream.FetchMaxWait(10*time.Second))
	if err != nil {
		l.t.Fatalf("fetch: %v", err)
	}
	var out []jetstream.Msg
	for m := range batch.Messages() {
		out = append(out, m)
	}
	if len(out) != k {
		l.t.Fatalf("fetched %d messages, want %d", len(out), k)
	}
	return out
}

// TestShutdownDrainsAndAcks: three messages are buffered inside the 1 s interval.
// Cancelling before the deadline drains them: they are stored and acked, and
// nothing is pending.
func TestShutdownDrainsAndAcks(t *testing.T) {
	l := newLoopEnv(t, defaultConsumerSpec(), 500)
	const n = 3
	l.publishN(n)

	offered := make(chan struct{})
	l.cons.seams.afterOffer = func(k int) {
		if k == n {
			close(offered)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := l.runWithCancel(ctx)

	select {
	case <-offered:
	case <-time.After(15 * time.Second):
		t.Fatal("consumer did not offer the three messages")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := l.rows(); got != n {
		t.Fatalf("rows after shutdown = %d, want %d", got, n)
	}
	l.waitAckPending()
}

// TestShutdownNaksUnprocessed: five messages are fetched and the context is
// cancelled after the second is offered. The two in the buffer are drained and
// stored. The three fetched but not offered are NAKed with no delay, so they
// redeliver at once with NumDelivered 2, and a second run stores them.
func TestShutdownNaksUnprocessed(t *testing.T) {
	l := newLoopEnv(t, defaultConsumerSpec(), 500)
	const n = 5
	l.publishN(n)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.cons.seams.afterOffer = func(k int) {
		if k == 2 {
			cancel()
		}
	}
	done := l.runWithCancel(ctx)
	if err := <-done; err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if got := l.rows(); got != 2 {
		t.Fatalf("rows after first shutdown = %d, want 2", got)
	}

	// Read the redelivered messages directly to check their delivery count.
	redelivered := l.fetchRaw(n - 2)
	for _, raw := range redelivered {
		md, err := raw.Metadata()
		if err != nil {
			t.Fatalf("metadata: %v", err)
		}
		if md.NumDelivered != 2 {
			t.Fatalf("redelivered NumDelivered = %d, want 2", md.NumDelivered)
		}
		if err := raw.Nak(); err != nil {
			t.Fatalf("nak for second run: %v", err)
		}
	}

	l.cons.seams.afterOffer = nil
	stop := l.start()
	l.waitRows(n)
	l.waitAckPending()
	if err := stop(); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if got := l.distinctMsgIDs(); got != n {
		t.Fatalf("distinct msg_id = %d, want %d", got, n)
	}
}
