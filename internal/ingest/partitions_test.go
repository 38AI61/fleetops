package ingest

import (
	"context"
	"database/sql"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"pgregory.net/rapid"

	"fleetops/internal/testinfra"
)

// childNames lists the positions children in name order.
func childNames(t testing.TB, conn *sql.DB) []string {
	t.Helper()
	rows, err := conn.Query(`
SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
WHERE i.inhparent = 'positions'::regclass ORDER BY c.relname`)
	if err != nil {
		t.Fatalf("list children: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan child: %v", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("children rows: %v", err)
	}
	return out
}

// monthNames returns positions_YYYY_MM for each month from first through last inclusive.
func monthNames(first, last time.Time) []string {
	var out []string
	for m := monthStart(first); !m.After(monthStart(last)); m = m.AddDate(0, 1, 0) {
		out = append(out, partitionName(m))
	}
	return out
}

// newPartitionMetrics returns a metric set on a private registry, so tests can read it back.
func newPartitionMetrics() (*Metrics, *prometheus.Registry) {
	reg := prometheus.NewRegistry()
	return NewMetrics(reg), reg
}

// gatherCounter reads one unlabelled counter from reg.
func gatherCounter(t testing.TB, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range families {
		if mf.GetName() == name {
			return mf.GetMetric()[0].GetCounter().GetValue()
		}
	}
	t.Fatalf("metric %s not gathered", name)
	return 0
}

func TestEnsureAheadCreatesCurrentPlusTwo(t *testing.T) {
	conn := testinfra.NewMigratedDB(t)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	if err := EnsurePartitions(context.Background(), conn, now); err != nil {
		t.Fatalf("EnsurePartitions: %v", err)
	}
	want := []string{"positions_2026_10", "positions_2026_11", "positions_2026_12"}
	if got := childNames(t, conn); !slices.Equal(got, want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
}

func TestEnsureIdempotent(t *testing.T) {
	conn := testinfra.NewMigratedDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	if err := EnsurePartitions(ctx, conn, now); err != nil {
		t.Fatalf("first EnsurePartitions: %v", err)
	}
	before := childNames(t, conn)
	created, err := ensurePartitions(ctx, conn, now)
	if err != nil {
		t.Fatalf("second ensurePartitions: %v", err)
	}
	if created != 0 {
		t.Fatalf("second pass created %d partitions, want 0", created)
	}
	if got := childNames(t, conn); !slices.Equal(got, before) {
		t.Fatalf("children changed on second pass: %v, was %v", got, before)
	}
}

func TestEnsureConcurrentCallers(t *testing.T) {
	conn := testinfra.NewMigratedDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

	const callers = 4
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = EnsurePartitions(ctx, conn, now)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	want := []string{"positions_2026_10", "positions_2026_11", "positions_2026_12"}
	if got := childNames(t, conn); !slices.Equal(got, want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
}

// insertAt writes one position row at serverTS into the vehicle and fleet of e.
func insertAt(t testing.TB, e *flushEnv, vehicleID int64, serverTS time.Time) {
	t.Helper()
	_, err := e.conn.Exec(`
INSERT INTO positions (msg_id, fleet_id, vehicle_id, geom, speed_kmh, heading_deg, accuracy_m, device_ts, server_ts)
VALUES ($1, $2, $3, ST_SetSRID(ST_MakePoint(29, 41), 4326)::geography, 0, 0, 5, $4, $4)`,
		uuid.New().String(), e.fleet, vehicleID, serverTS)
	if err != nil {
		t.Fatalf("insert at %s: %v", serverTS.Format("2006-01"), err)
	}
}

// TestRetentionDropsOnlyExpired is the roadmap P6 Validation item for D-14
// retention. The clock is flushBase (2026-10-25), so the cutoff is 2025-10-01:
// 2025-08 and 2025-09 are expired; 2025-10 through 2026-12 are retained.
func TestRetentionDropsOnlyExpired(t *testing.T) {
	e := newFlushEnv(t)
	ctx := context.Background()
	now := flushBase
	testinfra.EnsurePositionsPartitions(t, e.conn, time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))

	vehicleID := e.vehicle(1)
	insertAt(t, e, vehicleID, time.Date(2025, 8, 15, 0, 0, 0, 0, time.UTC))
	insertAt(t, e, vehicleID, time.Date(2025, 9, 15, 0, 0, 0, 0, time.UTC))
	insertAt(t, e, vehicleID, time.Date(2025, 10, 15, 0, 0, 0, 0, time.UTC))

	metrics, reg := newPartitionMetrics()
	m := &Maintenance{DB: e.conn, Clock: func() time.Time { return now }, Metrics: metrics}
	if err := m.RunOnce(ctx, now); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	wantRetained := monthNames(time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC))
	if got := childNames(t, e.conn); !slices.Equal(got, wantRetained) {
		t.Fatalf("retained children = %v, want %v", got, wantRetained)
	}
	if got := gatherCounter(t, reg, "fleetops_ingest_partitions_dropped_total"); got != 2 {
		t.Fatalf("partitions_dropped_total = %v, want 2", got)
	}
	if n := e.count(`SELECT count(*) FROM positions WHERE server_ts >= '2025-10-01' AND server_ts < '2025-11-01'`); n != 1 {
		t.Fatalf("rows in retained 2025-10 partition = %d, want 1", n)
	}
}

func TestRetentionNeverDropsCurrentOrNext(t *testing.T) {
	e := newFlushEnv(t)
	ctx := context.Background()
	// First instant of a month: the cutoff moves to 2025-11-01.
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	testinfra.EnsurePositionsPartitions(t, e.conn, time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC))

	metrics, _ := newPartitionMetrics()
	m := &Maintenance{DB: e.conn, Clock: func() time.Time { return now }, Metrics: metrics}
	if err := m.RunOnce(ctx, now); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	want := monthNames(time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	if got := childNames(t, e.conn); !slices.Equal(got, want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
}

// TestRetainableProperty: over random month sets and clocks, retainable returns
// exactly the children whose month is before the cutoff month.
func TestRetainableProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		offsets := rapid.SliceOfNDistinct(rapid.IntRange(0, 120), 0, 40, func(i int) int { return i }).Draw(rt, "offsets")
		var children []child
		for _, o := range offsets {
			m := base.AddDate(0, o, 0)
			children = append(children, child{name: partitionName(m), month: m})
		}
		now := base.Add(time.Duration(rapid.Int64Range(0, 130*365*24*3600).Draw(rt, "now_s")) * time.Second)
		cutoff := retainCutoff(now)

		got := retainable(children, cutoff)
		for _, c := range got {
			if !c.month.Before(cutoff) {
				rt.Fatalf("retainable returned %s, which is not before cutoff %s", c.name, cutoff)
			}
		}
		want := 0
		for _, c := range children {
			if c.month.Before(cutoff) {
				want++
			}
		}
		if len(got) != want {
			rt.Fatalf("retainable returned %d children, want %d (cutoff %s)", len(got), want, cutoff)
		}
	})
}

func TestMaintenanceRunsOnBoot(t *testing.T) {
	e := newFlushEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// One expired child exists before boot; the boot pass must drop it and create three.
	testinfra.EnsurePositionsPartitions(t, e.conn, time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC))
	metrics, reg := newPartitionMetrics()
	m := &Maintenance{DB: e.conn, Clock: func() time.Time { return flushBase }, Metrics: metrics}

	ready := make(chan struct{})
	var createdAtReady, droppedAtReady float64
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.Run(ctx, func() {
			createdAtReady = gatherCounter(t, reg, "fleetops_ingest_partitions_created_total")
			droppedAtReady = gatherCounter(t, reg, "fleetops_ingest_partitions_dropped_total")
			close(ready)
		})
	}()

	select {
	case <-ready:
	case <-time.After(30 * time.Second):
		t.Fatal("boot pass did not finish")
	}
	if createdAtReady != 2 || droppedAtReady != 1 {
		t.Fatalf("after boot pass: created %v, dropped %v; want 2 and 1", createdAtReady, droppedAtReady)
	}
	cancel()
	<-done
}
