package auth

import (
	"strings"
	"sync"
	"time"
)

// Login throttle defaults (D-04, GF-25): 5 failures per fixed 15-minute window.
const (
	defaultThrottleLimit  = 5
	defaultThrottleWindow = 15 * time.Minute
	throttleSweepEvery    = time.Minute
)

type throttleKey struct {
	email string
	ip    string
}

type throttleEntry struct {
	failures    int
	windowStart time.Time // time of the first failure in this window
}

// Throttle is an in-memory, single-instance login throttle (accepted risk per
// D-04). Key = (lowercased trimmed email, client IP). X-Forwarded-For is never
// read (GF-25).
type Throttle struct {
	mu        sync.Mutex
	now       func() time.Time
	limit     int
	window    time.Duration
	entries   map[throttleKey]throttleEntry
	lastSweep time.Time
}

// NewThrottle returns a throttle with the GF-25 defaults and an injected clock.
func NewThrottle(clock func() time.Time) *Throttle {
	return &Throttle{
		now:     clock,
		limit:   defaultThrottleLimit,
		window:  defaultThrottleWindow,
		entries: make(map[throttleKey]throttleEntry),
	}
}

// Allow reports whether a login attempt for (email, ip) may proceed. When it
// may not, retryAfter is the time left in the window. It runs before the
// password check, so a correct password is refused inside the window too.
func (t *Throttle) Allow(email, ip string) (retryAfter time.Duration, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	t.sweepLocked(now)

	e, found := t.entries[keyFor(email, ip)]
	if !found || !now.Before(e.windowStart.Add(t.window)) {
		return 0, true
	}
	if e.failures < t.limit {
		return 0, true
	}
	return e.windowStart.Add(t.window).Sub(now), false
}

// RecordFailure counts a failed login for (email, ip). The window starts at
// the first failure and is not extended by later ones.
func (t *Throttle) RecordFailure(email, ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	t.sweepLocked(now)

	k := keyFor(email, ip)
	e, found := t.entries[k]
	if !found || !now.Before(e.windowStart.Add(t.window)) {
		t.entries[k] = throttleEntry{failures: 1, windowStart: now}
		return
	}
	e.failures++
	t.entries[k] = e
}

// sweepLocked drops expired windows, at most once per throttleSweepEvery.
// Caller must hold t.mu.
func (t *Throttle) sweepLocked(now time.Time) {
	if !t.lastSweep.IsZero() && now.Sub(t.lastSweep) < throttleSweepEvery {
		return
	}
	t.lastSweep = now
	for k, e := range t.entries {
		if !now.Before(e.windowStart.Add(t.window)) {
			delete(t.entries, k)
		}
	}
}

func keyFor(email, ip string) throttleKey {
	return throttleKey{email: strings.ToLower(strings.TrimSpace(email)), ip: ip}
}
