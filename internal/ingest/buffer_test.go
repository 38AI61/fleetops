package ingest

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

var bufT0 = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

const (
	testInterval = time.Second
	testMax      = 500
	testAccuracy = 50.0
)

func newTestBuffer(c *Counters) *Buffer {
	return NewBuffer(testInterval, testMax, testAccuracy, c)
}

func msgWithAccuracy(acc float64) Message {
	return Message{
		MsgID:    uuid.New(),
		FleetID:  1,
		DeviceID: 1,
		Position: Position{Accuracy: acc, DeviceTS: bufT0},
		ServerTS: bufT0,
	}
}

func TestBufferOfferBuffersAcceptedMessage(t *testing.T) {
	b := newTestBuffer(&Counters{})
	buffered, batch := b.Offer(msgWithAccuracy(5), bufT0)
	if !buffered || batch != nil {
		t.Fatalf("Offer: buffered=%v batch=%v, want buffered and no batch", buffered, batch)
	}
	if _, ok := b.Deadline(); !ok {
		t.Fatal("accepted message must arm the deadline")
	}
}

func TestBufferAccuracyAboveMaxDroppedBeforeBuffer(t *testing.T) {
	c := &Counters{}
	b := newTestBuffer(c)
	buffered, batch := b.Offer(msgWithAccuracy(51), bufT0)
	if buffered || batch != nil {
		t.Fatalf("51 m: buffered=%v batch=%v, want dropped", buffered, batch)
	}
	if got := c.DroppedAccuracy.Load(); got != 1 {
		t.Fatalf("DroppedAccuracy = %d, want 1", got)
	}
	if got := b.Drain(); len(got) != 0 {
		t.Fatalf("buffer holds %d messages after a drop, want 0", len(got))
	}
	if _, ok := b.Deadline(); ok {
		t.Fatal("dropped message must not arm the deadline")
	}
}

func TestBufferAccuracyExactlyMaxKept(t *testing.T) {
	c := &Counters{}
	b := newTestBuffer(c)
	if buffered, _ := b.Offer(msgWithAccuracy(50.0), bufT0); !buffered {
		t.Fatal("50.0 m must be kept")
	}
	if c.DroppedAccuracy.Load() != 0 {
		t.Fatal("50.0 m must not count as a drop")
	}
}

func TestBufferDroppedMessageDoesNotArmTimer(t *testing.T) {
	b := newTestBuffer(&Counters{})
	b.Offer(msgWithAccuracy(60), bufT0)
	if _, ok := b.Deadline(); ok {
		t.Fatal("a dropped message armed the timer")
	}
	b.Offer(msgWithAccuracy(5), bufT0.Add(300*time.Millisecond))
	dl, ok := b.Deadline()
	if !ok || !dl.Equal(bufT0.Add(300*time.Millisecond+testInterval)) {
		t.Fatalf("deadline after the first accepted message = %v, %v", dl, ok)
	}
}

func TestBufferArmsOnFirstBufferedMessage(t *testing.T) {
	b := newTestBuffer(&Counters{})
	b.Offer(msgWithAccuracy(5), bufT0)
	b.Offer(msgWithAccuracy(5), bufT0.Add(400*time.Millisecond))
	dl, ok := b.Deadline()
	if !ok || !dl.Equal(bufT0.Add(testInterval)) {
		t.Fatalf("second message moved the deadline: %v, %v", dl, ok)
	}
}

func TestBufferTickBeforeDeadlineReturnsNil(t *testing.T) {
	b := newTestBuffer(&Counters{})
	b.Offer(msgWithAccuracy(5), bufT0)
	if got := b.Tick(bufT0.Add(testInterval - time.Nanosecond)); got != nil {
		t.Fatalf("Tick before deadline returned %d messages", len(got))
	}
}

func TestBufferTickAtDeadlineFlushes(t *testing.T) {
	b := newTestBuffer(&Counters{})
	b.Offer(msgWithAccuracy(5), bufT0)
	b.Offer(msgWithAccuracy(5), bufT0.Add(200*time.Millisecond))
	got := b.Tick(bufT0.Add(testInterval))
	if len(got) != 2 {
		t.Fatalf("Tick at deadline returned %d messages, want 2", len(got))
	}
	if _, ok := b.Deadline(); ok {
		t.Fatal("deadline still armed after a flush")
	}
}

func TestBufferFlushesAtMaxExactly500(t *testing.T) {
	b := newTestBuffer(&Counters{})
	for i := 1; i < testMax; i++ {
		if _, batch := b.Offer(msgWithAccuracy(5), bufT0); batch != nil {
			t.Fatalf("offer %d returned a batch of %d before the max", i, len(batch))
		}
	}
	_, batch := b.Offer(msgWithAccuracy(5), bufT0)
	if len(batch) != testMax {
		t.Fatalf("500th offer returned %d messages, want 500", len(batch))
	}
}

func TestBufferNeverReturnsMoreThanMax(t *testing.T) {
	b := newTestBuffer(&Counters{})
	var sizes []int
	for i := 0; i < 1200; i++ {
		if _, batch := b.Offer(msgWithAccuracy(5), bufT0); batch != nil {
			sizes = append(sizes, len(batch))
		}
	}
	sizes = append(sizes, len(b.Drain()))
	want := []int{500, 500, 200}
	if len(sizes) != len(want) {
		t.Fatalf("batch sizes %v, want %v", sizes, want)
	}
	for i := range want {
		if sizes[i] != want[i] || sizes[i] > testMax {
			t.Fatalf("batch sizes %v, want %v", sizes, want)
		}
	}
}

func TestBufferTimerRearmsAfterFlush(t *testing.T) {
	b := newTestBuffer(&Counters{})
	b.Offer(msgWithAccuracy(5), bufT0)
	b.Tick(bufT0.Add(testInterval))
	later := bufT0.Add(10 * time.Second)
	b.Offer(msgWithAccuracy(5), later)
	dl, ok := b.Deadline()
	if !ok || !dl.Equal(later.Add(testInterval)) {
		t.Fatalf("deadline after re-arm = %v, %v; want %v", dl, ok, later.Add(testInterval))
	}
}

func TestBufferDrainReturnsAllAndResets(t *testing.T) {
	b := newTestBuffer(&Counters{})
	var ids []uuid.UUID
	for i := 0; i < 3; i++ {
		m := msgWithAccuracy(5)
		ids = append(ids, m.MsgID)
		b.Offer(m, bufT0)
	}
	got := b.Drain()
	if len(got) != 3 {
		t.Fatalf("Drain returned %d messages, want 3", len(got))
	}
	for i := range ids {
		if got[i].MsgID != ids[i] {
			t.Fatalf("Drain order differs at %d", i)
		}
	}
	if again := b.Drain(); again != nil {
		t.Fatalf("second Drain returned %d messages, want nil", len(again))
	}
	if _, ok := b.Deadline(); ok {
		t.Fatal("deadline armed after Drain")
	}
}

func TestBufferDrainEmptyReturnsNil(t *testing.T) {
	if got := newTestBuffer(&Counters{}).Drain(); got != nil {
		t.Fatalf("Drain on empty buffer returned %v", got)
	}
}

func TestBufferConcurrentOfferTickDrain(t *testing.T) {
	const offers = 4000
	c := &Counters{}
	b := NewBuffer(testInterval, 97, testAccuracy, c)

	var mu sync.Mutex
	seen := map[uuid.UUID]int{}
	collect := func(batch []Message) {
		mu.Lock()
		defer mu.Unlock()
		for _, m := range batch {
			seen[m.MsgID]++
		}
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < offers; i++ {
			m := msgWithAccuracy(5)
			m.Position.DeviceTS = bufT0.Add(time.Duration(i) * time.Millisecond)
			if _, batch := b.Offer(m, bufT0.Add(time.Duration(i)*time.Millisecond)); batch != nil {
				collect(batch)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < offers; i++ {
			collect(b.Tick(bufT0.Add(time.Duration(i) * time.Millisecond)))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			collect(b.Drain())
		}
	}()
	wg.Wait()
	collect(b.Drain())

	total := 0
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("message %s returned %d times", id, n)
		}
		total++
	}
	if total != offers {
		t.Fatalf("returned %d distinct messages, want %d", total, offers)
	}
}
