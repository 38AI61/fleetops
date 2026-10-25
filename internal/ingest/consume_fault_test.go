package ingest

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// holdVehicleLatest takes ACCESS EXCLUSIVE on vehicles_latest in its own
// transaction. Every flush blocks on its upsert until release is called, so the
// fault is a real lock wait in PostgreSQL. release is safe to call twice.
func holdVehicleLatest(t testing.TB, conn *sql.DB) (release func()) {
	t.Helper()
	tx, err := conn.Begin()
	if err != nil {
		t.Fatalf("begin lock tx: %v", err)
	}
	if _, err := tx.Exec(`LOCK TABLE vehicles_latest IN ACCESS EXCLUSIVE MODE`); err != nil {
		_ = tx.Rollback()
		t.Fatalf("lock vehicles_latest: %v", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() { _ = tx.Rollback() })
	}
}

// killBlockedBackends terminates every backend of this database that is
// waiting on a lock, and returns how many it terminated. It polls until one is found.
func killBlockedBackends(t testing.TB, conn *sql.DB) int {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var killed int
		err := conn.QueryRow(`
SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity
WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()`).Scan(&killed)
		if err != nil {
			t.Fatalf("terminate blocked backends: %v", err)
		}
		if killed > 0 {
			return killed
		}
		if time.Now().After(deadline) {
			t.Fatal("no flush blocked on a lock after 15s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// distinctMsgIDs counts the distinct msg_id values in positions. Exactly-one-row
// holds when this equals the total row count and the expected message count.
func (l *loopEnv) distinctMsgIDs() int {
	return l.fe.count(`SELECT count(DISTINCT msg_id) FROM positions`)
}

// publishN publishes n live messages and returns their ids.
func (l *loopEnv) publishN(n int) []uuid.UUID {
	ids := make([]uuid.UUID, 0, n)
	for range n {
		m := l.liveMsg(time.Now().UTC())
		l.publish(m)
		ids = append(ids, m.MsgID)
	}
	return ids
}

// TestKillPGRedelivery is the roadmap P6 Validation item: a PostgreSQL backend is
// killed mid-flush. Every message is NAKed and redelivered, and each ends as
// exactly one row.
func TestKillPGRedelivery(t *testing.T) {
	l := newLoopEnv(t, defaultConsumerSpec(), 500)
	release := holdVehicleLatest(t, l.fe.conn)
	t.Cleanup(release)

	const n = 20
	l.publishN(n)
	stop := l.start()

	if killed := killBlockedBackends(t, l.fe.conn); killed != 1 {
		t.Fatalf("terminated %d backends, want 1 flush backend", killed)
	}
	release()

	l.waitRows(n)
	l.waitAckPending()
	if err := stop(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := l.distinctMsgIDs(); got != n {
		t.Fatalf("distinct msg_id = %d, want %d", got, n)
	}
	if got := counterValue(t, l.reg, "fleetops_ingest_flush_failures_total", "reason", "db"); got < 1 {
		t.Fatalf("flush_failures_total{reason=db} = %v, want >= 1", got)
	}
}

// TestFlushTimeoutNaks: a flush that outlives FlushTimeout fails as a timeout and
// its batch is NAKed. After the lock is released the redelivery stores each row once.
func TestFlushTimeoutNaks(t *testing.T) {
	spec := defaultConsumerSpec()
	spec.flushTimeout = 300 * time.Millisecond
	l := newLoopEnv(t, spec, 500)
	release := holdVehicleLatest(t, l.fe.conn)
	t.Cleanup(release)

	const n = 4
	l.publishN(n)
	stop := l.start()

	l.waitCounter("fleetops_ingest_flush_failures_total", "reason", "timeout", 1)
	release()

	l.waitRows(n)
	l.waitAckPending()
	if err := stop(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := l.distinctMsgIDs(); got != n {
		t.Fatalf("distinct msg_id = %d, want %d", got, n)
	}
}

// TestAckLostIsIdempotent: the first ack after a successful commit is lost. The
// message redelivers after AckWait, and ON CONFLICT leaves exactly one row.
func TestAckLostIsIdempotent(t *testing.T) {
	spec := defaultConsumerSpec()
	spec.ackWait = time.Second
	l := newLoopEnv(t, spec, 500)

	failed := false
	l.cons.seams.ackFault = func() error {
		if failed {
			return nil
		}
		failed = true
		return errors.New("injected ack fault")
	}

	const n = 3
	l.publishN(n)
	stop := l.start()

	l.waitCounter("fleetops_ingest_ack_errors_total", "", "", 1)
	l.waitRows(n)
	l.waitAckPending()
	if err := stop(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := l.rows(); got != n {
		t.Fatalf("rows = %d, want %d", got, n)
	}
	if got := l.distinctMsgIDs(); got != n {
		t.Fatalf("distinct msg_id = %d, want %d", got, n)
	}
}

// TestMaxDeliverTerminates: with the lock held for the whole test, every
// delivery fails. At MaxDeliver the messages are terminated and counted. The
// consumer keeps running, and a message sent after the lock is released is stored.
func TestMaxDeliverTerminates(t *testing.T) {
	spec := defaultConsumerSpec()
	spec.maxDeliver = 3
	spec.flushTimeout = 300 * time.Millisecond
	l := newLoopEnv(t, spec, 500)
	release := holdVehicleLatest(t, l.fe.conn)
	t.Cleanup(release)

	const n = 4
	l.publishN(n)
	stop := l.start()

	l.waitCounter("fleetops_ingest_terminated_total", "reason", "max_deliver", n)
	if got := l.rows(); got != 0 {
		t.Fatalf("rows = %d before release, want 0", got)
	}

	release()
	l.publishN(1)
	l.waitRows(1)
	if err := stop(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := counterValue(t, l.reg, "fleetops_ingest_terminated_total", "reason", "max_deliver"); got != n {
		t.Fatalf("terminated{max_deliver} = %v, want %d", got, n)
	}
}

// TestMalformedTerminates: a payload that does not parse is terminated on first
// delivery and counted, and the consumer keeps going.
func TestMalformedTerminates(t *testing.T) {
	l := newLoopEnv(t, defaultConsumerSpec(), 500)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := l.js.Publish(ctx, "gps.raw.1", []byte("not json")); err != nil {
		t.Fatalf("publish malformed: %v", err)
	}
	stop := l.start()

	l.waitCounter("fleetops_ingest_terminated_total", "reason", "malformed", 1)
	l.publishN(1)
	l.waitRows(1)
	l.waitAckPending()
	if err := stop(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := counterValue(t, l.reg, "fleetops_ingest_terminated_total", "reason", "malformed"); got != 1 {
		t.Fatalf("terminated{malformed} = %v, want 1", got)
	}
}

// TestMissingPartitionNakThenEnsure: a message stamped three months ahead has no
// partition. It fails with 23514, is NAKed and counted, and succeeds once
// EnsurePartitions runs at that clock.
func TestMissingPartitionNakThenEnsure(t *testing.T) {
	l := newLoopEnv(t, defaultConsumerSpec(), 500)
	future := time.Now().UTC().AddDate(0, 3, 0)
	m := l.liveMsg(future)
	l.publish(m)
	stop := l.start()

	l.waitCounter("fleetops_ingest_flush_failures_total", "reason", "missing_partition", 1)
	if got := counterValue(t, l.reg, "fleetops_ingest_missing_partition_total", "", ""); got < 1 {
		t.Fatalf("missing_partition_total = %v, want >= 1", got)
	}
	if got := l.rows(); got != 0 {
		t.Fatalf("rows = %d before ensure, want 0", got)
	}

	if err := EnsurePartitions(context.Background(), l.fe.conn, future); err != nil {
		t.Fatalf("EnsurePartitions at future clock: %v", err)
	}
	l.waitRows(1)
	l.waitAckPending()
	if err := stop(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := l.distinctMsgIDs(); got != 1 {
		t.Fatalf("distinct msg_id = %d, want 1", got)
	}
}
