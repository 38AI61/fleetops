package eta

import "time"

// AttemptWindow is the minimum gap between two OSRM calls for one job (D-35, GF-307).
const AttemptWindow = 5 * time.Minute

// Limiter allows one OSRM attempt per job per window (GF-307). It is in memory and
// single-writer: only the loop's pass calls it (GF-306). Attempts count on any outcome.
// A restart resets the map, which allows at most one extra call per job per process start.
type Limiter struct {
	window time.Duration
	last   map[int64]time.Time
}

// NewLimiter returns an empty limiter with the given window.
func NewLimiter(window time.Duration) *Limiter {
	return &Limiter{window: window, last: make(map[int64]time.Time)}
}

// Allow reports whether jobID may be attempted at now. When it returns true, the
// attempt is recorded before the call, so the window starts whatever the outcome.
func (l *Limiter) Allow(jobID int64, now time.Time) bool {
	if at, ok := l.last[jobID]; ok && now.Sub(at) < l.window {
		return false
	}
	l.last[jobID] = now
	return true
}

// Prune drops every job that is not in active. Each pass calls it with the active set (GF-307).
func (l *Limiter) Prune(active map[int64]struct{}) {
	for id := range l.last {
		if _, ok := active[id]; !ok {
			delete(l.last, id)
		}
	}
}
