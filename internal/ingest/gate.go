package ingest

import (
	"errors"
	"log"

	"github.com/nats-io/nats.go/jetstream"
)

// ErrBusy means the gate had no free slot, or the synchronous publish call
// failed before anything was queued. Both map to 503 busy (GF-60).
var ErrBusy = errors.New("ingest: publish gate busy")

// Gate bounds in-flight async publishes with a non-blocking slot pool (GF-60).
// The gate's slots are the only bound: the library's stall path is never reached
// because the JetStream context caps pending publishes at the same MaxInFlight.
type Gate struct {
	slots chan struct{}
}

// NewGate returns a gate with MaxInFlight free slots.
func NewGate() *Gate {
	return &Gate{slots: make(chan struct{}, MaxInFlight)}
}

// acquire takes a slot without waiting. It reports false when none is free.
func (g *Gate) acquire() bool {
	select {
	case g.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (g *Gate) release() {
	<-g.slots
}

// HoldSlots takes n slots without publishing and returns the function that
// gives them back. It is the test hook that fills the gate for the 503 case.
// It panics if fewer than n slots are free, so a test cannot run half-full.
func (g *Gate) HoldSlots(n int) (release func()) {
	for i := 0; i < n; i++ {
		if !g.acquire() {
			panic("ingest: HoldSlots: gate has fewer than n free slots")
		}
	}
	return func() {
		for i := 0; i < n; i++ {
			g.release()
		}
	}
}

// Publish queues m on its subject and returns once the publish is accepted by
// JetStream's async path. It does not wait for the ack, so the caller can return
// 204 before the ack (README §3). A failure after that point is logged with the
// msg id and fleet and is not retried (GF-61).
func (g *Gate) Publish(js jetstream.JetStream, m Message) error {
	_, err := g.publish(js, m)
	return err
}

// publish is Publish with the ack future exposed for tests.
func (g *Gate) publish(js jetstream.JetStream, m Message) (jetstream.PubAckFuture, error) {
	if !g.acquire() {
		return nil, ErrBusy
	}
	payload, err := m.Payload()
	if err != nil {
		g.release()
		return nil, err
	}
	fut, err := js.PublishAsync(m.Subject(), payload)
	if err != nil {
		g.release()
		return nil, ErrBusy
	}
	go g.awaitAck(fut, m)
	return fut, nil
}

// awaitAck frees the slot when the ack or the error arrives, and logs a failed
// publish with its msg id and fleet (GF-61).
func (g *Gate) awaitAck(fut jetstream.PubAckFuture, m Message) {
	defer g.release()
	select {
	case <-fut.Ok():
	case err := <-fut.Err():
		log.Printf("ingest: publish failed msg_id=%s fleet_id=%d: %v", m.MsgID, m.FleetID, err)
	}
}
