package ingest

import (
	"context"
	"database/sql"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/google/uuid"
	"pgregory.net/rapid"
)

// fataler is the failure surface shared by *testing.T and *rapid.T.
type fataler interface {
	Fatalf(format string, args ...any)
}

// propWindow is the fixed server_ts window (90 s, GF-82): some messages are
// stale at the flush time flushBase.
var propWindow = flushBase.Add(-90 * time.Second)

// propFixture is one migrated database with 10 paired vehicles and one unpaired
// device, built once per test function.
type propFixture struct {
	*flushEnv
	devices  []int64 // devices[k] is paired to vehicles[k]
	vehicle  map[int64]int64
	unpaired int64
}

func newPropFixture(t testing.TB) *propFixture {
	t.Helper()
	e := newFlushEnv(t)
	f := &propFixture{flushEnv: e, vehicle: map[int64]int64{}}
	for k := 0; k < 10; k++ {
		v, d := e.pairedVehicle(k)
		f.devices = append(f.devices, d)
		f.vehicle[d] = v
	}
	f.unpaired = e.device()
	return f
}

// reset empties the two tables a check writes to.
func (f *propFixture) reset(t fataler) {
	if _, err := f.conn.Exec(`TRUNCATE positions, vehicles_latest`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// rows returns t::text for every row of the query, in query order.
func rows(t fataler, conn *sql.DB, query string) []string {
	r, err := conn.Query(query)
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	defer func() { _ = r.Close() }()
	var out []string
	for r.Next() {
		var s string
		if err := r.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, s)
	}
	if err := r.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// snapshot is the byte-level state of both tables, ordered by primary key.
func snapshot(t fataler, conn *sql.DB) [2][]string {
	return [2][]string{
		rows(t, conn, `SELECT t::text FROM vehicles_latest t ORDER BY vehicle_id`),
		rows(t, conn, `SELECT t::text FROM positions t ORDER BY server_ts, msg_id`),
	}
}

// randomMessage draws one message: a paired or unpaired device, a server_ts in
// the window, an accuracy in [0, 100], and a random lat/lng/heading.
func (f *propFixture) randomMessage(rt *rapid.T, withUnpaired bool) Message {
	var dev int64
	if withUnpaired && rapid.IntRange(0, 11).Draw(rt, "device_or_unpaired") == 11 {
		dev = f.unpaired
	} else {
		dev = f.devices[rapid.IntRange(0, 9).Draw(rt, "device")]
	}
	offsetMs := rapid.Int64Range(0, 90_000).Draw(rt, "server_offset_ms")
	m := f.msg(dev, propWindow.Add(time.Duration(offsetMs)*time.Millisecond),
		realistic(rapid.Float64Range(-80, 80).Draw(rt, "lat")),
		realistic(rapid.Float64Range(-170, 170).Draw(rt, "lng")),
		realistic(rapid.Float64Range(0, 359.9).Draw(rt, "heading")))
	m.Position.Accuracy = realistic(rapid.Float64Range(0, 100).Draw(rt, "accuracy"))
	return m
}

// realistic snaps magnitudes below 1e-6 to zero. The DB properties write these
// values through REAL columns (GF-77 casts float8 to real). Below about 1.4e-45
// PostgreSQL raises "value out of range: underflow". Validate rejects values
// under 1e-45 at the edge (GF-85, validate.go); this snap keeps the generators
// well inside that floor.
func realistic(x float64) float64 {
	if math.Abs(x) < 1e-6 {
		return 0
	}
	return x
}

// TestFlushSemanticsDBProperty runs random streams through Buffer and then
// Writer.Flush on PostGIS (group 7b).
func TestFlushSemanticsDBProperty(t *testing.T) {
	f := newPropFixture(t)
	rapid.Check(t, func(rt *rapid.T) {
		f.reset(rt)
		c := &Counters{}
		b := NewBuffer(testInterval, testMax, testAccuracy, c)
		w := NewWriter(f.conn, c)
		ctx := context.Background()

		var history []Message
		var stored []Message
		var staleOrUnpaired, offers int

		flush := func(batch []Message) {
			res, err := w.Flush(ctx, batch, flushBase)
			if err != nil {
				rt.Fatalf("flush: %v", err)
			}
			if got := len(res.Stored) + res.Stale + res.Unpaired; got != len(batch) {
				rt.Fatalf("flush accounted %d of %d messages", got, len(batch))
			}
			stored = append(stored, res.Stored...)
			staleOrUnpaired += res.Stale + res.Unpaired
		}

		now := flushBase
		for i := 0; i < rapid.IntRange(1, 400).Draw(rt, "events"); i++ {
			now = now.Add(time.Duration(rapid.IntRange(0, 1500).Draw(rt, "advance_ms")) * time.Millisecond)
			if batch := b.Tick(now); batch != nil {
				flush(batch)
			}
			if rapid.Bool().Draw(rt, "offer") {
				var m Message
				if len(history) > 0 && rapid.Bool().Draw(rt, "redeliver") {
					m = history[rapid.IntRange(0, len(history)-1).Draw(rt, "redelivered_index")]
				} else {
					m = f.randomMessage(rt, true)
					history = append(history, m)
				}
				offers++
				if _, batch := b.Offer(m, now); batch != nil {
					flush(batch)
				}
			}
		}
		if rest := b.Drain(); rest != nil {
			flush(rest)
		}

		if got := c.Stored.Load() + c.Stale.Load() + c.Unpaired.Load() + c.DroppedAccuracy.Load(); got != int64(offers) {
			rt.Fatalf("counters sum to %d, want %d offers", got, offers)
		}
		if int(c.Stored.Load()) != len(stored) || int(c.Stale.Load()+c.Unpaired.Load()) != staleOrUnpaired {
			rt.Fatalf("counters disagree with flush results")
		}

		wantIDs := map[uuid.UUID]bool{}
		for _, m := range stored {
			wantIDs[m.MsgID] = true
		}
		gotRows := rows(rt, f.conn, `SELECT t::text FROM positions t`)
		if len(gotRows) != len(wantIDs) {
			rt.Fatalf("positions rows = %d, want %d distinct stored messages", len(gotRows), len(wantIDs))
		}
		for _, id := range rows(rt, f.conn, `SELECT msg_id::text FROM positions`) {
			if !wantIDs[uuid.MustParse(id)] {
				rt.Fatalf("positions holds %s, which was not stored", id)
			}
		}

		latest := map[int64]time.Time{}
		for _, m := range stored {
			v := f.vehicle[m.DeviceID]
			if m.ServerTS.After(latest[v]) {
				latest[v] = m.ServerTS
			}
		}
		for v, want := range latest {
			var got time.Time
			if err := f.conn.QueryRow(`SELECT server_ts FROM vehicles_latest WHERE vehicle_id = $1`, v).Scan(&got); err != nil {
				rt.Fatalf("vehicles_latest for %d: %v", v, err)
			}
			if !got.Equal(want) {
				rt.Fatalf("vehicles_latest.server_ts for %d = %v, want %v", v, got, want)
			}
		}
		if n := f.count(`SELECT count(*) FROM vehicles_latest`); n != len(latest) {
			rt.Fatalf("vehicles_latest rows = %d, want %d", n, len(latest))
		}
	})
}

// TestBatchUpsertPermutationProperty checks that any permutation of a batch
// yields the same vehicles_latest and positions (group 7b).
func TestBatchUpsertPermutationProperty(t *testing.T) {
	f := newPropFixture(t)
	rapid.Check(t, func(rt *rapid.T) {
		f.reset(rt)
		batch := f.randomBatch(rt)
		seed := rapid.Uint64().Draw(rt, "seed")

		c := &Counters{}
		if _, err := NewWriter(f.conn, c).Flush(context.Background(), batch, flushBase); err != nil {
			rt.Fatalf("flush original: %v", err)
		}
		want := snapshot(rt, f.conn)

		f.reset(rt)
		perm := rand.New(rand.NewSource(int64(seed))).Perm(len(batch)) //nolint:gosec // seeded test permutation, not security
		shuffled := make([]Message, len(batch))
		for i, j := range perm {
			shuffled[i] = batch[j]
		}
		if _, err := NewWriter(f.conn, &Counters{}).Flush(context.Background(), shuffled, flushBase); err != nil {
			rt.Fatalf("flush permutation: %v", err)
		}
		if got := snapshot(rt, f.conn); !equalSnapshots(want, got) {
			rt.Fatalf("permutation (seed %d) changed the stored state", seed)
		}
	})
}

// TestBatchUpsertReplayProperty checks that replaying a batch on the same
// Writer is a no-op for both tables and for the heading state (group 7b).
func TestBatchUpsertReplayProperty(t *testing.T) {
	f := newPropFixture(t)
	rapid.Check(t, func(rt *rapid.T) {
		f.reset(rt)
		batch := f.randomBatch(rt)
		w := NewWriter(f.conn, &Counters{})
		ctx := context.Background()

		if _, err := w.Flush(ctx, batch, flushBase); err != nil {
			rt.Fatalf("first flush: %v", err)
		}
		first := snapshot(rt, f.conn)
		smoothBefore := make(map[int64]smoothState, len(w.smooth))
		for k, v := range w.smooth {
			smoothBefore[k] = v
		}

		if _, err := w.Flush(ctx, batch, flushBase); err != nil {
			rt.Fatalf("replay flush: %v", err)
		}
		if got := snapshot(rt, f.conn); !equalSnapshots(first, got) {
			rt.Fatalf("replay changed the stored state")
		}
		if len(w.smooth) != len(smoothBefore) {
			rt.Fatalf("replay changed the heading state size")
		}
		for k, v := range smoothBefore {
			if w.smooth[k] != v {
				rt.Fatalf("replay moved heading state for vehicle %d", k)
			}
		}
	})
}

// randomBatch draws 1 to 200 paired messages across 1 to 10 vehicles.
func (f *propFixture) randomBatch(rt *rapid.T) []Message {
	n := rapid.IntRange(1, 200).Draw(rt, "batch_size")
	vehicles := rapid.IntRange(1, 10).Draw(rt, "vehicles")
	batch := make([]Message, 0, n)
	for i := 0; i < n; i++ {
		m := f.randomMessage(rt, false)
		m.DeviceID = f.devices[rapid.IntRange(0, vehicles-1).Draw(rt, "vehicle_index")]
		m.Position.Accuracy = 5
		batch = append(batch, m)
	}
	return batch
}

func equalSnapshots(a, b [2][]string) bool {
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}
