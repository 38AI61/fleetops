// Package dispatch serves the dispatcher's nearest-van read (P13, GF-209). It
// reads the registry, pairings and positions and never mutates them.
package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// nearestSQL is GF-210 and GF-211. The eligibility joins are the whole filter:
// an active vehicle, a latest position, an open pairing with a driver, and that
// driver in the same fleet. ORDER BY is exact, so the distance and the order agree.
// $1 fleet, $2 lat, $3 lng, $4 limit.
const nearestSQL = `
WITH pt AS (SELECT ST_SetSRID(ST_MakePoint($3, $2), 4326)::geography AS g)
SELECT v.id, v.plate, d.id, d.name,
       round(ST_Distance(vl.geom, pt.g)::numeric, 1)::float8,
       ST_Y(vl.geom::geometry), ST_X(vl.geom::geometry), vl.server_ts
FROM vehicles v
JOIN vehicles_latest vl ON vl.vehicle_id = v.id AND vl.fleet_id = $1
JOIN vehicle_pairings p ON p.vehicle_id = v.id AND p.ended_at IS NULL AND p.driver_id IS NOT NULL
JOIN drivers d ON d.id = p.driver_id AND d.fleet_id = $1
CROSS JOIN pt
WHERE v.fleet_id = $1 AND v.status = 'active'
ORDER BY ST_Distance(vl.geom, pt.g) ASC, vl.vehicle_id ASC
LIMIT $4`

// ErrNotFound means the job is missing or belongs to another fleet (GF-213).
var ErrNotFound = errors.New("dispatch: not found")

// pickupSQL reads the job's pickup in the caller's fleet only (GF-213). There is no state gate.
const pickupSQL = `
SELECT ST_Y(pickup::geometry), ST_X(pickup::geometry)
FROM jobs WHERE id = $1 AND fleet_id = $2`

// Store reads nearest candidates from the caller's database.
type Store struct {
	DB *sql.DB
}

// Candidate is one row of GF-214. DistanceM is rounded to 0.1 m in SQL.
type Candidate struct {
	VehicleID  int64
	Plate      string
	DriverID   int64
	DriverName string
	DistanceM  float64
	Lat        float64
	Lng        float64
	ServerTS   time.Time
}

// Nearest returns up to limit eligible vehicles of fleetID, nearest to (lat, lng)
// first (GF-209, GF-211). Ties break by vehicle id. The caller validates limit.
func (s *Store) Nearest(ctx context.Context, fleetID int64, lat, lng float64, limit int) ([]Candidate, error) {
	rows, err := s.DB.QueryContext(ctx, nearestSQL, fleetID, lat, lng, limit)
	if err != nil {
		return nil, fmt.Errorf("nearest query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []Candidate{}
	for rows.Next() {
		var c Candidate
		if err := rows.Scan(&c.VehicleID, &c.Plate, &c.DriverID, &c.DriverName, &c.DistanceM, &c.Lat, &c.Lng, &c.ServerTS); err != nil {
			return nil, fmt.Errorf("scan nearest row: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("nearest rows: %w", err)
	}
	return out, nil
}

// JobPickup returns the pickup of job jobID in fleetID as (lat, lng). A missing
// or cross-fleet job is ErrNotFound.
func (s *Store) JobPickup(ctx context.Context, fleetID, jobID int64) (float64, float64, error) {
	var lat, lng float64
	err := s.DB.QueryRowContext(ctx, pickupSQL, jobID, fleetID).Scan(&lat, &lng)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, ErrNotFound
	}
	if err != nil {
		return 0, 0, fmt.Errorf("job pickup: %w", err)
	}
	return lat, lng, nil
}
