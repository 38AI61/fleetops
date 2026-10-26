package simulator

import "time"

// Slot is one due position for one vehicle: the index k and the instant
// start + offset(vehicle) + k*rate (GF-144).
type Slot struct {
	Vehicle int
	Index   int64
	DueAt   time.Time
}

// Scheduler is the pacing clock (GF-144, GF-145, GF-148). It is a pure value:
// it reads no clock and starts no goroutines. Callers pass the instant to
// Due, so tests step it with synthetic times.
type Scheduler struct {
	start   time.Time
	rate    time.Duration
	n       int
	next    []int64 // next unsent slot index per vehicle
	skipped []int64 // slots dropped by the catch-up rule, per vehicle
}

// NewScheduler paces n vehicles at one position per rate each. Offsets spread
// the vehicles evenly across the interval (GF-144).
func NewScheduler(start time.Time, rate time.Duration, n int) *Scheduler {
	return &Scheduler{
		start:   start,
		rate:    rate,
		n:       n,
		next:    make([]int64, n),
		skipped: make([]int64, n),
	}
}

// offset is vehicle i's phase within the interval: i*rate/N.
func (s *Scheduler) offset(i int) time.Duration {
	return s.rate * time.Duration(i) / time.Duration(s.n)
}

// due is the instant of slot k for vehicle i.
func (s *Scheduler) due(i int, k int64) time.Time {
	return s.start.Add(s.offset(i) + time.Duration(k)*s.rate)
}

// Due returns, per vehicle, at most one slot: the newest slot whose instant is
// at or before now (GF-145). Older unsent slots are dropped and counted as
// skipped, so a late process never bursts. The next due slot is the first one
// after now.
func (s *Scheduler) Due(now time.Time) []Slot {
	var out []Slot
	for i := range s.next {
		elapsed := now.Sub(s.start) - s.offset(i)
		if elapsed < 0 {
			continue
		}
		latest := int64(elapsed / s.rate)
		if latest < s.next[i] {
			continue
		}
		s.skipped[i] += latest - s.next[i]
		out = append(out, Slot{Vehicle: i, Index: latest, DueAt: s.due(i, latest)})
		s.next[i] = latest + 1
	}
	return out
}

// NextDue is the earliest instant at which any vehicle has a slot to send.
func (s *Scheduler) NextDue() time.Time {
	var earliest time.Time
	for i := range s.next {
		at := s.due(i, s.next[i])
		if i == 0 || at.Before(earliest) {
			earliest = at
		}
	}
	return earliest
}

// Skipped returns how many slots vehicle i dropped under the catch-up rule.
func (s *Scheduler) Skipped(i int) int64 {
	return s.skipped[i]
}
