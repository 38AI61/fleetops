package ingest

import (
	"testing"
	"time"
)

// TestConsumerHappyPath: N gateway messages end as N rows, and nothing is left unacked.
func TestConsumerHappyPath(t *testing.T) {
	l := newLoopEnv(t, defaultConsumerSpec(), 500)
	const n = 50
	for range n {
		l.publish(l.liveMsg(time.Now().UTC()))
	}
	stop := l.start()

	l.waitRows(n)
	if err := stop(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	l.waitAckPending()
	if got := l.rows(); got != n {
		t.Fatalf("rows = %d, want %d", got, n)
	}
}

// TestConsumerAcksOnlyAfterCommit: at ack time the batch is already committed.
// The seam runs after COMMIT and before the acks.
func TestConsumerAcksOnlyAfterCommit(t *testing.T) {
	l := newLoopEnv(t, defaultConsumerSpec(), 10)
	const n = 35
	var flushed int
	l.cons.seams.beforeAck = func(batch []Message) {
		flushed += len(batch)
		if got := l.rows(); got != flushed {
			t.Errorf("at ack time rows = %d, want %d (batch of %d)", got, flushed, len(batch))
		}
	}
	for range n {
		l.publish(l.liveMsg(time.Now().UTC()))
	}
	stop := l.start()

	l.waitRows(n)
	if err := stop(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	l.waitAckPending()
}

// TestConsumerAccuracyDropAckedImmediately: a message over the accuracy ceiling
// is acked at Offer and never reaches the database.
func TestConsumerAccuracyDropAckedImmediately(t *testing.T) {
	l := newLoopEnv(t, defaultConsumerSpec(), 500)
	for range 3 {
		l.publish(l.liveMsg(time.Now().UTC()))
	}
	for range 2 {
		m := l.liveMsg(time.Now().UTC())
		m.Position.Accuracy = 60
		l.publish(m)
	}
	stop := l.start()

	l.waitCounter("fleetops_ingest_messages_total", "outcome", "dropped_accuracy", 2)
	l.waitRows(3)
	if err := stop(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	l.waitAckPending()
	if got := l.rows(); got != 3 {
		t.Fatalf("rows = %d, want 3", got)
	}
}

// TestConsumerStaleAndUnpairedAcked: a stale message and an unpaired device's
// message are acked after the batch commits and never written.
func TestConsumerStaleAndUnpairedAcked(t *testing.T) {
	l := newLoopEnv(t, defaultConsumerSpec(), 500)
	stale := l.liveMsg(time.Now().UTC().Add(-61 * time.Second))
	unpaired := l.liveMsg(time.Now().UTC())
	unpaired.DeviceID = l.fe.device()
	l.publish(stale)
	l.publish(unpaired)
	stop := l.start()

	l.waitCounter("fleetops_ingest_messages_total", "outcome", "stale", 1)
	l.waitCounter("fleetops_ingest_messages_total", "outcome", "unpaired", 1)
	if err := stop(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	l.waitAckPending()
	if got := l.rows(); got != 0 {
		t.Fatalf("rows = %d, want 0", got)
	}
}

// TestConsumerResultMismatchNaks: a FlushResult that accounts for fewer messages
// than the batch takes the NAK path. The redelivery then stores each message once.
func TestConsumerResultMismatchNaks(t *testing.T) {
	l := newLoopEnv(t, defaultConsumerSpec(), 500)
	const n = 5
	for range n {
		l.publish(l.liveMsg(time.Now().UTC()))
	}
	faulted := false
	l.cons.seams.flushResult = func(res FlushResult) FlushResult {
		if faulted || len(res.Stored) == 0 {
			return res
		}
		faulted = true
		res.Stored = res.Stored[:len(res.Stored)-1]
		return res
	}
	stop := l.start()

	l.waitCounter("fleetops_ingest_flush_failures_total", "reason", "result_mismatch", 1)
	l.waitRows(n)
	// The NAKed batch redelivers after 1 s; wait for its ack while the consumer runs.
	l.waitAckPending()
	if err := stop(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := l.rows(); got != n {
		t.Fatalf("rows = %d, want %d", got, n)
	}
}
