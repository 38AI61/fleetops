package ingest

import "testing"

func TestGateRejectsWhenFull(t *testing.T) {
	g := NewGate()
	for i := 0; i < MaxInFlight; i++ {
		if !g.acquire() {
			t.Fatalf("slot %d of %d refused on an empty gate", i+1, MaxInFlight)
		}
	}
	if g.acquire() {
		t.Fatal("slot beyond MaxInFlight granted; the gate must refuse without waiting")
	}
	g.release()
	if !g.acquire() {
		t.Fatal("no slot after one release")
	}
}

func TestGateHoldSlotsFillsAndReleases(t *testing.T) {
	g := NewGate()
	release := g.HoldSlots(MaxInFlight)
	if g.acquire() {
		t.Fatal("full gate granted a slot")
	}
	release()
	if !g.acquire() {
		t.Fatal("no slot after HoldSlots release")
	}
}
