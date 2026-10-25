package fanout

// PositionBatchMax is the most points one position.batch message carries (GF-102).
// It is a client contract, not a tunable (D-22).
const PositionBatchMax = 200

// Batch is one single-fleet run of points, at most PositionBatchMax long.
type Batch struct {
	FleetID int64
	Points  []Point
}

// PlanPositionBatches groups points by fleet and splits each group into
// consecutive chunks of at most max points (GF-102). Fleets appear in
// first-appearance order, points keep their input order within a fleet, and a
// chunk never spans fleets. It panics if max < 1.
func PlanPositionBatches(points []Point, max int) []Batch {
	if max < 1 {
		panic("fanout: PlanPositionBatches max must be at least 1")
	}

	index := map[int64]int{}
	var fleets []int64
	var groups [][]Point
	for _, p := range points {
		i, ok := index[p.FleetID]
		if !ok {
			i = len(fleets)
			index[p.FleetID] = i
			fleets = append(fleets, p.FleetID)
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], p)
	}

	var out []Batch
	for i, g := range groups {
		for start := 0; start < len(g); start += max {
			end := min(start+max, len(g))
			out = append(out, Batch{FleetID: fleets[i], Points: g[start:end:end]})
		}
	}
	return out
}
