package ingest

import "sync/atomic"

// Counters are the P5 write-side counts (GF-83). The Buffer and the Writer share
// one value by pointer. Metrics registration is P6 and P22, not here.
type Counters struct {
	Stored          atomic.Int64
	DroppedAccuracy atomic.Int64
	Stale           atomic.Int64
	Unpaired        atomic.Int64
}
