package alerts

import (
	"sort"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// episodeEvent is one report or one tick on a single vehicle's timeline (GF-292).
type episodeEvent struct {
	at     time.Time
	report bool
}

// episodeAlert records that the episode keyed by lastSeen alerted at tick at.
type episodeAlert struct {
	key time.Time
	at  time.Time
}

// orderEvents sorts by time. A report precedes a tick at the same instant, so a
// tick at a new report's time belongs to the new episode (oracle: L <= t < N).
func orderEvents(events []episodeEvent) {
	sort.SliceStable(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if !a.at.Equal(b.at) {
			return a.at.Before(b.at)
		}
		return a.report && !b.report
	})
}

// simulate runs the silence rule over the timeline. The keys map is the in-memory
// mirror of the SQL NOT EXISTS: a key is written once it alerts (GF-292, GF-294).
func simulate(events []episodeEvent, threshold time.Duration) []episodeAlert {
	keys := map[time.Time]bool{}
	var cur time.Time
	haveReport := false
	var out []episodeAlert
	for _, ev := range events {
		if ev.report {
			cur, haveReport = ev.at, true
			continue
		}
		if !haveReport {
			continue
		}
		if decideSilence(cur, ev.at, threshold, keys[cur]) {
			keys[cur] = true
			out = append(out, episodeAlert{key: cur, at: ev.at})
		}
	}
	return out
}

// oracle is the independent model (GF-292): for each report L with next report N
// (or none), the episode alerts iff some tick t has L <= t < N and t - L > threshold,
// and it alerts at the first such tick.
func oracle(reports, ticks []time.Time, threshold time.Duration) map[time.Time]time.Time {
	out := map[time.Time]time.Time{}
	for i, l := range reports {
		hasNext := i+1 < len(reports)
		var n time.Time
		if hasNext {
			n = reports[i+1]
		}
		for _, t := range ticks {
			if t.Before(l) {
				continue
			}
			if hasNext && !t.Before(n) {
				break
			}
			if t.Sub(l) > threshold {
				out[l] = t
				break
			}
		}
	}
	return out
}

// TestSilenceEpisodesProperty: for random report and tick timelines, at most one
// alert per episode key, the alerted keys equal the oracle, and each alert fires
// at the first observing tick past the threshold. A new report starts a new
// episode that can alert again. Seed policy (G5): rapid draws a fresh seed per
// run and prints it on failure, for -rapid.seed=<n>.
func TestSilenceEpisodesProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		threshold := time.Duration(rapid.IntRange(60, 900).Draw(t, "threshold_s")) * time.Second
		reportGaps := rapid.SliceOfN(rapid.IntRange(1, 1800), 1, 12).Draw(t, "report_gap_s")
		tickGaps := rapid.SliceOfN(rapid.IntRange(1, 900), 1, 60).Draw(t, "tick_gap_s")

		var events []episodeEvent
		var reports, ticks []time.Time

		at := base
		reports = append(reports, at)
		events = append(events, episodeEvent{at: at, report: true})
		for _, g := range reportGaps {
			at = at.Add(time.Duration(g) * time.Second)
			reports = append(reports, at)
			events = append(events, episodeEvent{at: at, report: true})
		}
		at = base
		for _, g := range tickGaps {
			at = at.Add(time.Duration(g) * time.Second)
			ticks = append(ticks, at)
			events = append(events, episodeEvent{at: at})
		}
		orderEvents(events)

		got := simulate(events, threshold)
		want := oracle(reports, ticks, threshold)

		seen := map[time.Time]bool{}
		for _, a := range got {
			if seen[a.key] {
				t.Fatalf("episode %s alerted twice", a.key)
			}
			seen[a.key] = true
			if a.at.Sub(a.key) <= threshold {
				t.Fatalf("episode %s alerted at %s, not past the threshold %s", a.key, a.at, threshold)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("alerts = %d, oracle = %d", len(got), len(want))
		}
		for _, a := range got {
			w, ok := want[a.key]
			if !ok {
				t.Fatalf("episode %s alerted, oracle says it should not", a.key)
			}
			if !w.Equal(a.at) {
				t.Fatalf("episode %s alerted at %s, oracle first tick %s", a.key, a.at, w)
			}
		}
	})
}

// TestSilenceEpisodesFixed: three hand-written streams run on every -count=1 pass.
// Offsets are from base.
func TestSilenceEpisodesFixed(t *testing.T) {
	const min = time.Minute
	cases := []struct {
		name      string
		threshold time.Duration
		reports   []time.Duration
		ticks     []time.Duration
		want      map[time.Duration]time.Duration // report offset -> alert tick offset
	}{
		{
			// A silence alerts, the same episode stays quiet, and a new report re-arms.
			name:      "rearm after a report",
			threshold: 10 * min,
			reports:   []time.Duration{0, 21 * min},
			ticks:     []time.Duration{5 * min, 10 * min, 10*min + time.Second, 20 * min, 25 * min, 31*min + time.Second, 40 * min},
			want: map[time.Duration]time.Duration{
				0:        10*min + time.Second,
				21 * min: 31*min + time.Second,
			},
		},
		{
			// Exactly the threshold does not alert; one second past it does.
			name:      "threshold boundary",
			threshold: 10 * min,
			reports:   []time.Duration{0},
			ticks:     []time.Duration{10 * min, 10*min + time.Second, 30 * min},
			want:      map[time.Duration]time.Duration{0: 10*min + time.Second},
		},
		{
			// A tick at the new report's instant belongs to the new episode, which then alerts later.
			name:      "report at the tick instant",
			threshold: 10 * min,
			reports:   []time.Duration{0, 15 * min},
			ticks:     []time.Duration{10*min + time.Second, 15 * min, 25*min + time.Second},
			want: map[time.Duration]time.Duration{
				0:        10*min + time.Second,
				15 * min: 25*min + time.Second,
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var events []episodeEvent
			var reports, ticks []time.Time
			for _, r := range c.reports {
				reports = append(reports, base.Add(r))
				events = append(events, episodeEvent{at: base.Add(r), report: true})
			}
			for _, k := range c.ticks {
				ticks = append(ticks, base.Add(k))
				events = append(events, episodeEvent{at: base.Add(k)})
			}
			orderEvents(events)

			got := simulate(events, c.threshold)
			if len(got) != len(c.want) {
				t.Fatalf("alerts = %+v, want %d", got, len(c.want))
			}
			for _, a := range got {
				wantAt, ok := c.want[a.key.Sub(base)]
				if !ok || !a.at.Equal(base.Add(wantAt)) {
					t.Fatalf("alert %s at %s, want %+v", a.key.Sub(base), a.at.Sub(base), c.want)
				}
			}
			if o := oracle(reports, ticks, c.threshold); len(o) != len(c.want) {
				t.Fatalf("oracle disagrees with the fixed case: %d alerts", len(o))
			}
		})
	}
}
