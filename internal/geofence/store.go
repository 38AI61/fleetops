package geofence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/lib/pq"
)

// Store holds the geofence SQL. Every query is scoped by fleet_id (GF-269).
type Store struct {
	DB *sql.DB
}

// Create writes one fence in one transaction (GF-264, GF-267). Validity runs
// inside that transaction, so an invalid polygon stores nothing. The polygon
// is normalized to counter-clockwise (RFC 7946 right-hand rule). The bbox is
// computed here and never taken from the client (GF-262).
func (s *Store) Create(ctx context.Context, fleetID int64, in Input) (Feature, error) {
	geojson, err := in.geometryJSON()
	if err != nil {
		return Feature{}, err
	}
	var schedule sql.NullString
	if in.Schedule != nil {
		b, err := json.Marshal(in.Schedule)
		if err != nil {
			return Feature{}, err
		}
		schedule = sql.NullString{String: string(b), Valid: true}
	}
	var radius sql.NullFloat64
	var dwell sql.NullInt64
	if in.Kind == KindCircle {
		radius = sql.NullFloat64{Float64: in.RadiusM, Valid: true}
	}
	if in.DwellMin != nil {
		dwell = sql.NullInt64{Int64: int64(*in.DwellMin), Valid: true}
	}
	// Polygon boxes come from ST_Envelope in SQL. Circle boxes come from the Go helper.
	var minLng, minLat, maxLng, maxLat float64
	if in.Kind == KindCircle {
		minLng, minLat, maxLng, maxLat = CircleBBox(in.Centre[0], in.Centre[1], in.RadiusM)
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Feature{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT ST_IsValid(ST_GeomFromGeoJSON($1)::geometry)`, geojson).Scan(&valid); err != nil {
		return Feature{}, err
	}
	if !valid {
		return Feature{}, bad("geometry invalid")
	}

	var id int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO geofences (fleet_id, name, kind, geom, radius_m, bbox, dwell_min, schedule)
		VALUES (
			$1, $2, $3,
			ST_ForcePolygonCCW(ST_GeomFromGeoJSON($4))::geography,
			$5,
			CASE WHEN $3 = 'polygon'
				THEN ST_Envelope(ST_GeomFromGeoJSON($4))
				ELSE ST_MakeEnvelope($6, $7, $8, $9, 4326)
			END,
			$10, $11::jsonb)
		RETURNING id`,
		fleetID, in.Name, in.Kind, geojson, radius,
		minLng, minLat, maxLng, maxLat, dwell, schedule,
	).Scan(&id)
	if err != nil {
		if isCode(err, "23505") {
			return Feature{}, ErrConflict
		}
		return Feature{}, err
	}
	f, err := readOne(ctx, tx, fleetID, id)
	if err != nil {
		return Feature{}, err
	}
	if err := tx.Commit(); err != nil {
		return Feature{}, err
	}
	return f, nil
}

// List returns every fence in the fleet, ordered by id (GF-263). There is no
// pagination and no per-fleet cap in P17.
func (s *Store) List(ctx context.Context, fleetID int64) (Collection, error) {
	rows, err := s.DB.QueryContext(ctx, selectSQL+` WHERE fleet_id = $1 ORDER BY id ASC`, fleetID)
	if err != nil {
		return Collection{}, err
	}
	defer func() { _ = rows.Close() }()
	out := Collection{Type: "FeatureCollection", Features: []Feature{}}
	for rows.Next() {
		f, err := scanFence(rows)
		if err != nil {
			return Collection{}, err
		}
		out.Features = append(out.Features, f)
	}
	if err := rows.Err(); err != nil {
		return Collection{}, err
	}
	return out, nil
}

// Delete hard-deletes one fence (GF-267). A fence with events fails the
// ON DELETE RESTRICT foreign key (23503), which is a 409. A fence outside the
// fleet is ErrNotFound (GF-269).
func (s *Store) Delete(ctx context.Context, fleetID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM geofences WHERE id = $1 AND fleet_id = $2`, id, fleetID)
	if err != nil {
		if isCode(err, "23503") {
			return ErrConflict
		}
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// selectSQL reads a fence in the wire shape. Coordinates use 15 digits so
// posted values round-trip exactly (GF-263). The bbox comes from its four edges.
const selectSQL = `
	SELECT id, name, kind, ST_AsGeoJSON(geom::geometry, 15), radius_m, dwell_min, schedule::text,
	       ST_XMin(bbox), ST_YMin(bbox), ST_XMax(bbox), ST_YMax(bbox)
	FROM geofences`

// readOne reads one fence inside the caller's transaction.
func readOne(ctx context.Context, tx *sql.Tx, fleetID, id int64) (Feature, error) {
	row := tx.QueryRowContext(ctx, selectSQL+` WHERE id = $1 AND fleet_id = $2`, id, fleetID)
	return scanFence(row)
}

type scanner interface {
	Scan(dest ...any) error
}

func scanFence(s scanner) (Feature, error) {
	var (
		f        Feature
		geom     string
		radius   sql.NullFloat64
		dwell    sql.NullInt64
		schedule sql.NullString
		bbox     [4]float64
	)
	if err := s.Scan(&f.ID, &f.Properties.Name, &f.Properties.Kind, &geom, &radius, &dwell, &schedule,
		&bbox[0], &bbox[1], &bbox[2], &bbox[3]); err != nil {
		return Feature{}, err
	}
	f.Type = "Feature"
	f.Geometry = json.RawMessage(geom)
	f.BBox = bbox[:]
	if radius.Valid {
		r := radius.Float64
		f.Properties.RadiusM = &r
	}
	if dwell.Valid {
		d := int(dwell.Int64)
		f.Properties.DwellMin = &d
	}
	if schedule.Valid {
		var sc Schedule
		if err := json.Unmarshal([]byte(schedule.String), &sc); err != nil {
			return Feature{}, err
		}
		f.Properties.Schedule = &sc
	}
	return f, nil
}

// isCode reports whether err is a Postgres error with the given SQLSTATE.
func isCode(err error, code string) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && string(pqErr.Code) == code
}
