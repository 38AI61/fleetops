package ingest

import (
	"math"
	"time"
)

// headingAlpha is the EMA weight of each new sample (README §13, GF-78).
const headingAlpha = 0.5

// isStale reports whether a message is older than 60 s at `now` (GF-72). The
// comparison is strict: age exactly 60 s is kept, and a future server_ts is kept.
func isStale(serverTS, now time.Time) bool {
	return now.Sub(serverTS) > staleAfter
}

// headingStep moves prev toward raw by headingAlpha along the shorter arc (GF-78).
// The result is in [0, 360).
func headingStep(prev, raw float64) float64 {
	delta := math.Mod(raw-prev+540, 360) - 180
	return math.Mod(prev+headingAlpha*delta+360, 360)
}

// smoothState is one vehicle's heading EMA. The zero value is unseeded.
type smoothState struct {
	heading float64
	at      time.Time
}

// next applies one fresh sample. The first sample seeds the state with its raw
// heading. A sample whose server_ts is not after `at` is a replay and leaves the
// state unchanged (GF-78).
func (s smoothState) next(raw float64, serverTS time.Time) smoothState {
	if s.at.IsZero() {
		return smoothState{heading: raw, at: serverTS}
	}
	if !serverTS.After(s.at) {
		return s
	}
	return smoothState{heading: headingStep(s.heading, raw), at: serverTS}
}
