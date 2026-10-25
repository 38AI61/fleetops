package ingest

import (
	"context"
	"sort"
	"testing"
	"time"

	"fleetops/internal/testinfra"
)

// TestIngestThroughputSmoke100PerSecond drives the P6 consumer role on PostGIS:
// one message per 10 ms for 10 s, Tick every iteration, flush each batch (group 8).
// Latency is logged, not gated (P23 gates it).
func TestIngestThroughputSmoke100PerSecond(t *testing.T) {
	e := newFlushEnv(t)
	testinfra.EnsurePositionsPartitions(t, e.conn, time.Now(), time.Now())
	_, device := e.pairedVehicle(0)

	ctx := context.Background()
	b := newTestBuffer(e.counters)

	var offers, flushes, maxBatch int
	var latencies []time.Duration
	flushOne := func(batch []Message) {
		t0 := time.Now()
		res, err := e.writer.Flush(ctx, batch, time.Now())
		latencies = append(latencies, time.Since(t0))
		if err != nil {
			t.Fatalf("flush %d: %v", flushes, err)
		}
		if len(res.Stored) != len(batch) {
			t.Fatalf("flush %d stored %d of %d", flushes, len(res.Stored), len(batch))
		}
		flushes++
		if len(batch) > maxBatch {
			maxBatch = len(batch)
		}
		if n := e.count(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND state = 'idle in transaction'`); n != 0 {
			t.Fatalf("flush %d left %d idle-in-transaction backends", flushes, n)
		}
	}

	start := time.Now()
	end := start.Add(10 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
loop:
	for range ticker.C {
		now := time.Now()
		if now.After(end) {
			break loop
		}
		m := e.msg(device, now.UTC(), 41.0, 29.0, 10)
		m.Position.Accuracy = 5
		offers++
		if _, batch := b.Offer(m, now); batch != nil {
			flushOne(batch)
		}
		if batch := b.Tick(now); batch != nil {
			flushOne(batch)
		}
	}
	if rest := b.Drain(); rest != nil {
		flushOne(rest)
	}
	elapsed := time.Since(start)

	stored := e.counters.Stored.Load()
	if stored != int64(offers) {
		t.Fatalf("Stored = %d, offers = %d (zero loss expected)", stored, offers)
	}
	if e.counters.Stale.Load() != 0 || e.counters.Unpaired.Load() != 0 || e.counters.DroppedAccuracy.Load() != 0 {
		t.Fatalf("stale %d, unpaired %d, accuracy drops %d; want all zero",
			e.counters.Stale.Load(), e.counters.Unpaired.Load(), e.counters.DroppedAccuracy.Load())
	}
	if maxBatch > testMax {
		t.Fatalf("max batch %d exceeds %d", maxBatch, testMax)
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p := func(q float64) time.Duration { return latencies[int(q*float64(len(latencies)-1))] }
	t.Logf("offers=%d stored=%d stale=0 unpaired=0 dropped=0 elapsed=%s msgs/s=%.1f flushes=%d max_batch=%d p50=%s p99=%s",
		offers, stored, elapsed.Round(time.Millisecond), float64(offers)/elapsed.Seconds(), flushes, maxBatch, p(0.50), p(0.99))
}
