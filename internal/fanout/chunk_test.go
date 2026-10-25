package fanout

import (
	"encoding/binary"
	"slices"
	"testing"

	"github.com/google/uuid"
	"pgregory.net/rapid"
)

// genPoints draws 0–1,000 points over 1–5 fleets. Lat carries the input index,
// so every point is identifiable; msg_id values repeat on purpose (GF-102).
func genPoints(rt *rapid.T) []Point {
	n := rapid.IntRange(0, 1000).Draw(rt, "n")
	fleets := rapid.Int64Range(1, 5).Draw(rt, "fleets")
	points := make([]Point, n)
	for i := range points {
		var id uuid.UUID
		binary.BigEndian.PutUint64(id[:8], rapid.Uint64Range(0, 5).Draw(rt, "msg"))
		points[i] = Point{
			MsgID:    id,
			FleetID:  rapid.Int64Range(1, fleets).Draw(rt, "fleet"),
			DeviceID: 1,
			Lat:      float64(i),
		}
	}
	return points
}

func TestChunkPropertyEveryPointOnceOrderMax200(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		points := genPoints(rt)
		maxPer := rapid.IntRange(1, 250).Draw(rt, "max")
		batches := PlanPositionBatches(points, maxPer)

		seen := make([]int, len(points))
		perFleet := map[int64][]Point{}
		for _, b := range batches {
			if len(b.Points) < 1 || len(b.Points) > maxPer {
				rt.Fatalf("batch size %d outside 1..%d", len(b.Points), maxPer)
			}
			for _, p := range b.Points {
				if p.FleetID != b.FleetID {
					rt.Fatalf("batch for fleet %d holds a point of fleet %d", b.FleetID, p.FleetID)
				}
				seen[int(p.Lat)]++
				perFleet[b.FleetID] = append(perFleet[b.FleetID], p)
			}
		}
		for i, n := range seen {
			if n != 1 {
				rt.Fatalf("input index %d appears %d times, want 1", i, n)
			}
		}
		for _, fleet := range fleetsOf(points) {
			var want []Point
			for _, p := range points {
				if p.FleetID == fleet {
					want = append(want, p)
				}
			}
			if !slices.Equal(perFleet[fleet], want) {
				rt.Fatalf("fleet %d order differs from input", fleet)
			}
		}
	})
}

// fleetsOf lists fleet ids in first-appearance order.
func fleetsOf(points []Point) []int64 {
	var out []int64
	seen := map[int64]bool{}
	for _, p := range points {
		if !seen[p.FleetID] {
			seen[p.FleetID] = true
			out = append(out, p.FleetID)
		}
	}
	return out
}

func TestChunkBoundary200And201(t *testing.T) {
	one := func(n int) []Point {
		points := make([]Point, n)
		for i := range points {
			points[i] = Point{FleetID: 7, Lat: float64(i)}
		}
		return points
	}

	got := PlanPositionBatches(one(200), PositionBatchMax)
	if len(got) != 1 || len(got[0].Points) != 200 {
		t.Fatalf("200 points: got %d batches, want 1 of 200", len(got))
	}

	got = PlanPositionBatches(one(201), PositionBatchMax)
	if len(got) != 2 || len(got[0].Points) != 200 || len(got[1].Points) != 1 {
		t.Fatalf("201 points: got %d batches, want sizes 200 then 1", len(got))
	}
	if got[1].Points[0].Lat != 200 {
		t.Fatalf("second batch starts at input index %v, want 200", got[1].Points[0].Lat)
	}
}
