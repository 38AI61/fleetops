package reports

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"testing"
	"time"

	"fleetops/internal/ingest"
)

// propertyFleetDays is the number of seeded fleet-days (D-37, bounded).
const propertyFleetDays = 25

// pairRec is one generated pairing. driver 0 is a driverless pairing. Records are
// kept in insertion order, which is the id order the tie-break uses.
type pairRec struct {
	vehicle int64
	driver  int64
	started time.Time
	ended   *time.Time
}

// jobRec is one generated delivered job.
type jobRec struct {
	driver    int64
	delivered time.Time
	win       *[2]time.Time
}

// refDriver is the GF-321 pairing rule: among pairings of the vehicle active at
// at (started <= at, open or at < ended) with a driver, the latest started_at
// wins, and a tie goes to the later record.
func refDriver(pairs []pairRec, vehicle int64, at time.Time) (int64, bool) {
	var best *pairRec
	for i := range pairs {
		p := &pairs[i]
		if p.vehicle != vehicle || p.driver == 0 || p.started.After(at) {
			continue
		}
		if p.ended != nil && !at.Before(*p.ended) {
			continue
		}
		if best == nil || !p.started.Before(best.started) {
			best = p
		}
	}
	if best == nil {
		return 0, false
	}
	return best.driver, true
}

// reference is the Go model of GF-321 for one fleet-day. It reads samples and
// jobs as they were inserted and never touches SQL.
func reference(loc *time.Location, day time.Time, samples []pos, pairs []pairRec, jobs []jobRec) []stat {
	start, end := DayBounds(loc, day)
	byVehicle := map[int64][]pos{}
	for _, s := range samples {
		if !s.ts.Before(start) && s.ts.Before(end) {
			byVehicle[s.vehicle] = append(byVehicle[s.vehicle], s)
		}
	}
	acc := map[int64]*stat{}
	get := func(d int64) *stat {
		if acc[d] == nil {
			acc[d] = &stat{DriverID: d}
		}
		return acc[d]
	}
	for _, ps := range byVehicle {
		sort.Slice(ps, func(i, j int) bool { return ps[i].ts.Before(ps[j].ts) })
		for i := 1; i < len(ps); i++ {
			prev, cur := ps[i-1], ps[i]
			gap := cur.ts.Sub(prev.ts)
			if gap > maxGap {
				continue
			}
			drv, ok := refDriver(pairs, prev.vehicle, prev.ts)
			if !ok {
				continue
			}
			s := get(drv)
			s.DistanceM += haversineM(prev.lat, prev.lng, cur.lat, cur.lng)
			if prev.speed < idleBelowKmh {
				s.IdleS += int(gap / time.Second)
			}
		}
	}
	for _, j := range jobs {
		if j.delivered.Before(start) || !j.delivered.Before(end) {
			continue
		}
		s := get(j.driver)
		s.JobsCompleted++
		if j.win != nil {
			s.OnTimeDenom++
			if !j.delivered.After(j.win[1]) {
				s.OnTimeCount++
			}
		}
	}
	ids := make([]int64, 0, len(acc))
	for id := range acc {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]stat, 0, len(ids))
	for _, id := range ids {
		s := *acc[id]
		s.DistanceM = math.Round(s.DistanceM*10) / 10
		out = append(out, s)
	}
	return out
}

// TestPropertyRollupMatchesReference: 25 seeded fleet-days, 6 vehicles each, random
// sample gaps (including 300 s and 301 s), speeds (including 0.99 and 1.00),
// pairing changes with driverless spans, and delivered jobs with and without windows.
// SQL output must equal the Go model. Counts are exact. Distance agrees to 0.15 m,
// because SQL and Go round the sum at different points.
func TestPropertyRollupMatchesReference(t *testing.T) {
	f := newFx(t)
	ctx := context.Background()
	// Feb, Mar and Apr partitions: samples start 30 min before midnight on day 1.
	if err := ingest.EnsurePartitions(ctx, f.db, civil(2026, time.February, 1)); err != nil {
		t.Fatalf("partitions: %v", err)
	}
	rng := rand.New(rand.NewPCG(20260308, 21)) //nolint:gosec // seeded test stream, not a credential
	zones := []string{"UTC", "America/New_York", "Europe/Berlin"}
	gaps := []int{30, 60, 240, 300, 301, 900, 3600}
	speeds := []float64{0, 0.5, 0.99, 1.0, 1.5, 12, 30}
	rows := 0

	for i := 0; i < propertyFleetDays; i++ {
		tz := zones[rng.IntN(len(zones))]
		loc := mustLoad(tz)
		day := civil(2026, time.March, 1+rng.IntN(31))
		start, end := DayBounds(loc, day)
		fleet := f.fleet(fmt.Sprintf("prop-%d", i), tz)

		drivers := make([]int64, 4)
		for k := range drivers {
			drivers[k] = f.driver(fleet, fmt.Sprintf("P%d-%d", i, k))
		}

		var samples []pos
		var pairs []pairRec
		var jobs []jobRec
		var vehicles []int64
		for v := 0; v < 6; v++ {
			vid := f.vehicle(fleet)
			vehicles = append(vehicles, vid)

			// Pairings: up to three, with random start times, each ending where the next begins.
			n := rng.IntN(4)
			starts := make([]time.Time, n)
			for j := range starts {
				starts[j] = start.Add(-3*time.Hour + time.Duration(rng.Int64N(int64(27*time.Hour))))
			}
			sort.Slice(starts, func(a, b int) bool { return starts[a].Before(starts[b]) })
			for j := range starts {
				var driver int64
				if rng.IntN(5) != 0 {
					driver = drivers[rng.IntN(len(drivers))]
				}
				var ended *time.Time
				if j+1 < n {
					e := starts[j+1]
					ended = &e
				}
				f.pair(fleet, vid, driver, starts[j], ended)
				pairs = append(pairs, pairRec{vehicle: vid, driver: driver, started: starts[j], ended: ended})
			}

			// Samples from 30 min before the window to 30 min after, so the clip at midnight is exercised.
			ts := start.Add(-30*time.Minute + time.Duration(rng.IntN(3600))*time.Second)
			lat := 52.0 + rng.Float64()*0.5
			for ts.Before(end.Add(30 * time.Minute)) {
				speed := speeds[rng.IntN(len(speeds))]
				samples = append(samples, pos{fleet: fleet, vehicle: vid, ts: ts, lat: lat, lng: 13.4, speed: speed})
				lat += (rng.Float64() - 0.5) * 0.004
				ts = ts.Add(time.Duration(gaps[rng.IntN(len(gaps))]) * time.Second)
			}
		}

		// Delivered jobs, some with a window: each delivery falls in the day, a window may end before or after it.
		for k := rng.IntN(5); k > 0; k-- {
			delivered := start.Add(time.Duration(rng.Int64N(int64(24 * time.Hour))))
			driver := drivers[rng.IntN(len(drivers))]
			vid := vehicles[rng.IntN(len(vehicles))]
			var win *[2]time.Time
			if rng.IntN(2) == 0 {
				w := [2]time.Time{
					delivered.Add(-time.Hour),
					delivered.Add(-30*time.Minute + time.Duration(rng.Int64N(int64(2*time.Hour)))),
				}
				win = &w
			}
			f.job(fleet, "delivered", vid, driver, win, &delivered)
			jobs = append(jobs, jobRec{driver: driver, delivered: delivered, win: win})
		}

		f.positions(samples)
		f.rollup(fleet, loc, day)
		got := f.stats(fleet, day)
		want := reference(loc, day, samples, pairs, jobs)
		rows += len(want)

		if len(got) != len(want) {
			t.Fatalf("fleet %d (%s, %s): %d rows, want %d\n got %+v\nwant %+v", i, tz, day.Format(time.DateOnly), len(got), len(want), got, want)
		}
		for k := range want {
			g, w := got[k], want[k]
			if math.Abs(g.DistanceM-w.DistanceM) > 0.15 {
				t.Fatalf("fleet %d driver %d distance = %v, want %v", i, w.DriverID, g.DistanceM, w.DistanceM)
			}
			g.DistanceM, w.DistanceM = 0, 0
			if g != w {
				t.Fatalf("fleet %d (%s, %s) row %d = %+v, want %+v", i, tz, day.Format(time.DateOnly), k, g, w)
			}
		}
	}
	if rows == 0 {
		t.Fatal("property run produced no rows; the generator is not exercising the rules")
	}
}
