package ingest

import (
	"sync"
	"time"
)

// staleAfter is the at-flush age limit (GF-72, D-18).
const staleAfter = 60 * time.Second

// Buffer is the batch state machine (GF-70 to GF-73). It owns no goroutine,
// timer, or I/O: the caller offers messages, ticks, and drains. Callers pass
// `now` explicitly, so the trigger logic runs without a clock.
type Buffer struct {
	flushInterval time.Duration
	flushMax      int
	accuracyMaxM  float64
	counters      *Counters

	mu      sync.Mutex
	pending []Message
	armedAt time.Time
}

// NewBuffer returns an empty buffer with the FLEETOPS_* tunables (GF-80).
func NewBuffer(flushInterval time.Duration, flushMax int, accuracyMaxM float64, c *Counters) *Buffer {
	return &Buffer{
		flushInterval: flushInterval,
		flushMax:      flushMax,
		accuracyMaxM:  accuracyMaxM,
		counters:      c,
	}
}

// Offer accepts one message. A message above the accuracy ceiling is dropped
// before buffering and does not arm the timer (GF-71). An accepted message arms
// the timer when the buffer was empty (GF-73). When the buffer reaches flushMax,
// Offer returns the batch.
func (b *Buffer) Offer(m Message, now time.Time) (buffered bool, batch []Message) {
	if m.Position.Accuracy > b.accuracyMaxM {
		b.counters.DroppedAccuracy.Add(1)
		return false, nil
	}
	m.ServerTS = m.ServerTS.Truncate(time.Microsecond)
	m.Position.DeviceTS = m.Position.DeviceTS.Truncate(time.Microsecond)

	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pending) == 0 {
		b.armedAt = now
	}
	b.pending = append(b.pending, m)
	if len(b.pending) >= b.flushMax {
		return true, b.take()
	}
	return true, nil
}

// Deadline reports when the interval trigger fires, if it is armed (GF-73).
func (b *Buffer) Deadline() (time.Time, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pending) == 0 {
		return time.Time{}, false
	}
	return b.armedAt.Add(b.flushInterval), true
}

// Tick returns the batch when the interval trigger has fired at `now`. The
// comparison is inclusive: now == deadline flushes (GF-73).
func (b *Buffer) Tick(now time.Time) []Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pending) == 0 || now.Before(b.armedAt.Add(b.flushInterval)) {
		return nil
	}
	return b.take()
}

// Drain returns every buffered message and resets the buffer (shutdown path).
func (b *Buffer) Drain() []Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.take()
}

// take hands the pending slice to the caller and resets the timer. Callers
// hold b.mu.
func (b *Buffer) take() []Message {
	if len(b.pending) == 0 {
		return nil
	}
	batch := b.pending
	b.pending = nil
	b.armedAt = time.Time{}
	return batch
}
