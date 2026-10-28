package eta

import (
	"testing"
	"time"
)

// TestSlipBoundary: slip is strict. Projected exactly at window_end + 15 min does not slip;
// one second past does (D-34, GF-310).
func TestSlipBoundary(t *testing.T) {
	now := loopBase
	// window_end + 15 min is now + 30 min, so a 30-minute ETA lands exactly on the boundary.
	windowEnd := now.Add(15 * time.Minute)
	const etaToBoundary = 30

	if Slip(now, etaToBoundary, &windowEnd) {
		t.Errorf("projected exactly at window_end+15m: slip = true, want false")
	}
	justBefore := windowEnd.Add(-time.Second)
	if !Slip(now, etaToBoundary, &justBefore) {
		t.Errorf("projected one second past window_end+15m: slip = false, want true")
	}
}

// TestSlipWindowlessNever: a job with no window never slips, however large the ETA (GF-310).
func TestSlipWindowlessNever(t *testing.T) {
	if Slip(loopBase, 1440, nil) {
		t.Error("windowless job with ETA 1440: slip = true, want false")
	}
}

// TestSlipZeroETA: an ETA of 0 slips only when now itself is past window_end + 15 min.
func TestSlipZeroETA(t *testing.T) {
	past := loopBase.Add(-SlipThreshold - time.Second)
	if !Slip(loopBase, 0, &past) {
		t.Error("ETA 0, window ended 15 min and 1 s ago: slip = false, want true")
	}
	onBoundary := loopBase.Add(-SlipThreshold)
	if Slip(loopBase, 0, &onBoundary) {
		t.Error("ETA 0, window ended exactly 15 min ago: slip = true, want false")
	}
}
