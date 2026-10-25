package auth

import (
	"fmt"
	"testing"
	"time"
)

func newThrottleWithClock(t *testing.T) (*Throttle, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	return NewThrottle(clock.now), clock
}

func TestThrottleBlocksSixthAttemptAfterFiveFailures(t *testing.T) {
	th, _ := newThrottleWithClock(t)
	for i := 0; i < 5; i++ {
		if _, ok := th.Allow("a@example.com", "10.0.0.1"); !ok {
			t.Fatalf("attempt %d refused before any failure reached the limit", i+1)
		}
		th.RecordFailure("a@example.com", "10.0.0.1")
	}
	retry, ok := th.Allow("a@example.com", "10.0.0.1")
	if ok {
		t.Fatal("sixth check after five failures: want refusal")
	}
	if retry <= 0 {
		t.Fatalf("retryAfter = %v, want > 0", retry)
	}
}

func TestThrottleRetryAfterIsRemainingWindow(t *testing.T) {
	th, clock := newThrottleWithClock(t)
	for i := 0; i < 5; i++ {
		th.RecordFailure("a@example.com", "10.0.0.1")
	}
	clock.t = clock.t.Add(4 * time.Minute)
	retry, ok := th.Allow("a@example.com", "10.0.0.1")
	if ok {
		t.Fatal("inside window: want refusal")
	}
	if want := 11 * time.Minute; retry != want {
		t.Fatalf("retryAfter = %v, want %v", retry, want)
	}
}

func TestThrottleKeysAreIndependentPerIPAndEmail(t *testing.T) {
	th, _ := newThrottleWithClock(t)
	for i := 0; i < 5; i++ {
		th.RecordFailure("a@example.com", "10.0.0.1")
	}
	if _, ok := th.Allow("a@example.com", "10.0.0.2"); !ok {
		t.Fatal("same email, other IP: want allowed")
	}
	if _, ok := th.Allow("b@example.com", "10.0.0.1"); !ok {
		t.Fatal("other email, same IP: want allowed")
	}
}

func TestThrottleEmailIsCaseInsensitiveAndTrimmed(t *testing.T) {
	th, _ := newThrottleWithClock(t)
	for i := 0; i < 5; i++ {
		th.RecordFailure("  A@Example.com ", "10.0.0.1")
	}
	if _, ok := th.Allow("a@example.com", "10.0.0.1"); ok {
		t.Fatal("case/space variant of same email: want refusal")
	}
}

func TestThrottleWindowExpiryReallows(t *testing.T) {
	th, clock := newThrottleWithClock(t)
	for i := 0; i < 5; i++ {
		th.RecordFailure("a@example.com", "10.0.0.1")
	}
	clock.t = clock.t.Add(15 * time.Minute)
	if _, ok := th.Allow("a@example.com", "10.0.0.1"); !ok {
		t.Fatal("after 15 min: want allowed again")
	}
}

func TestThrottleWindowStartsAtFirstFailure(t *testing.T) {
	th, clock := newThrottleWithClock(t)
	th.RecordFailure("a@example.com", "10.0.0.1")
	clock.t = clock.t.Add(10 * time.Minute)
	for i := 0; i < 4; i++ {
		th.RecordFailure("a@example.com", "10.0.0.1")
	}
	// Five failures, but the window opened 10 minutes ago: 5 min remain.
	retry, ok := th.Allow("a@example.com", "10.0.0.1")
	if ok || retry != 5*time.Minute {
		t.Fatalf("ok=%v retry=%v, want refused with 5m", ok, retry)
	}
}

func TestThrottleSweepRemovesExpiredEntries(t *testing.T) {
	th, clock := newThrottleWithClock(t)
	for i := 0; i < 3; i++ {
		th.RecordFailure(fmt.Sprintf("u%d@example.com", i), "10.0.0.1")
	}
	if n := len(th.entries); n != 3 {
		t.Fatalf("entries = %d, want 3", n)
	}
	clock.t = clock.t.Add(16 * time.Minute)
	th.Allow("fresh@example.com", "10.0.0.9")
	if n := len(th.entries); n != 0 {
		t.Fatalf("after sweep entries = %d, want 0", n)
	}
}
