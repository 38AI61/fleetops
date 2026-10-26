package mapview

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// inBoxSQL is GF-128. ST_Intersects on geography uses vehicles_latest_geom_gix,
// and fleet_id = $1 uses vehicles_latest_fleet_idx.
const inBoxSQL = `
SELECT vehicle_id, ST_Y(geom::geometry), ST_X(geom::geometry), speed_kmh, heading_deg, device_ts, server_ts
FROM vehicles_latest
WHERE fleet_id = $1
  AND ST_Intersects(geom, ST_MakeEnvelope($2, $3, $4, $5, 4326)::geography)
ORDER BY vehicle_id
LIMIT $6`

// Row is one vehicles_latest row inside a box. Speed and heading are null when the column is.
type Row struct {
	VehicleID  int64
	Lat        float64
	Lng        float64
	SpeedKmh   sql.NullFloat64
	HeadingDeg sql.NullFloat64
	DeviceTS   time.Time
	ServerTS   time.Time
}

// InBox returns up to limit latest positions of fleetID inside b, ordered by
// vehicle_id (GF-128). The caller passes the cap plus one to detect truncation (GF-129).
func InBox(ctx context.Context, db *sql.DB, fleetID int64, b BBox, limit int) ([]Row, error) {
	rows, err := db.QueryContext(ctx, inBoxSQL, fleetID, b.MinLng, b.MinLat, b.MaxLng, b.MaxLat, limit)
	if err != nil {
		return nil, fmt.Errorf("map in-box query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.VehicleID, &r.Lat, &r.Lng, &r.SpeedKmh, &r.HeadingDeg, &r.DeviceTS, &r.ServerTS); err != nil {
			return nil, fmt.Errorf("scan map row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("map rows: %w", err)
	}
	return out, nil
}
