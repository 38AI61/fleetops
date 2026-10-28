package alerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Sentinels for the store. The HTTP layer maps each to one apierr code.
var (
	ErrNotFound = errors.New("alerts: not found")
	ErrConflict = errors.New("alerts: conflict")
)

// Store holds the alert SQL. Every query is scoped by fleet_id (GF-298).
type Store struct {
	DB *sql.DB
}

// Alert is the wire shape of one alerts row (GF-298).
type Alert struct {
	ID       int64           `json:"id"`
	FleetID  int64           `json:"fleet_id"`
	Kind     string          `json:"kind"`
	Severity string          `json:"severity"`
	Payload  json.RawMessage `json:"payload"`
	RaisedAt time.Time       `json:"raised_at"`
	AckedBy  *int64          `json:"acked_by"`
	AckedAt  *time.Time      `json:"acked_at"`
}

// Page is the GET /alerts body. Unacked counts every unacked alert in the fleet,
// whatever the since filter (GF-298).
type Page struct {
	Alerts  []Alert `json:"alerts"`
	Unacked int64   `json:"unacked"`
}

// Rule is one alert_rules row as the API returns it (GF-298). Evaluated is
// false for eta_slip, which has no evaluator until P20 (GF-290).
type Rule struct {
	Kind      string          `json:"kind"`
	Enabled   bool            `json:"enabled"`
	Params    json.RawMessage `json:"params"`
	UpdatedAt time.Time       `json:"updated_at"`
	Evaluated bool            `json:"evaluated"`
}

const alertCols = `id, fleet_id, kind, severity, payload, raised_at, acked_by, acked_at`

// List returns the fleet's alerts raised at or after since, newest first, capped
// at limit (GF-298). The unacked count is fleet-wide and ignores since.
func (s *Store) List(ctx context.Context, fleetID int64, since time.Time, limit int) (Page, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT `+alertCols+` FROM alerts
		WHERE fleet_id = $1 AND raised_at >= $2
		ORDER BY raised_at DESC, id DESC
		LIMIT $3`, fleetID, since, limit)
	if err != nil {
		return Page{}, err
	}
	defer func() { _ = rows.Close() }()
	out := Page{Alerts: []Alert{}}
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return Page{}, err
		}
		out.Alerts = append(out.Alerts, a)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	if err := s.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM alerts WHERE fleet_id = $1 AND acked_at IS NULL`, fleetID,
	).Scan(&out.Unacked); err != nil {
		return Page{}, err
	}
	return out, nil
}

// Ack is the one-time transition (GF-295). The UPDATE only matches an unacked
// alert in the fleet. Zero rows is ErrNotFound when the id is absent from the
// fleet, otherwise ErrConflict. The trigger is the backstop.
func (s *Store) Ack(ctx context.Context, fleetID, id, userID int64) (Alert, error) {
	row := s.DB.QueryRowContext(ctx, `
		UPDATE alerts SET acked_by = $3, acked_at = now()
		WHERE id = $1 AND fleet_id = $2 AND acked_at IS NULL
		RETURNING `+alertCols, id, fleetID, userID)
	a, err := scanAlert(row)
	if err == nil {
		return a, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Alert{}, err
	}
	var exists bool
	if err := s.DB.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM alerts WHERE id = $1 AND fleet_id = $2)`, id, fleetID,
	).Scan(&exists); err != nil {
		return Alert{}, err
	}
	if !exists {
		return Alert{}, ErrNotFound
	}
	return Alert{}, ErrConflict
}

// ListRules returns the fleet's rules in seed order (GF-297, GF-298).
func (s *Store) ListRules(ctx context.Context, fleetID int64) ([]Rule, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT kind, enabled, params, updated_at FROM alert_rules
		WHERE fleet_id = $1 ORDER BY id`, fleetID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PatchRules applies the validated patches in one transaction and returns the
// full rule list (GF-297, GF-298). A kind with no row in the fleet is ErrNotFound.
func (s *Store) PatchRules(ctx context.Context, fleetID int64, patches []RulePatch) ([]Rule, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	for _, p := range patches {
		var enabled, params any
		if p.Enabled != nil {
			enabled = *p.Enabled
		}
		if p.Params != nil {
			params = string(p.Params)
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE alert_rules
			SET enabled = COALESCE($3::boolean, enabled),
			    params = COALESCE($4::jsonb, params),
			    updated_at = now()
			WHERE fleet_id = $1 AND kind = $2`, fleetID, p.Kind, enabled, params)
		if err != nil {
			return nil, err
		}
		if n, err := res.RowsAffected(); err != nil {
			return nil, err
		} else if n == 0 {
			return nil, ErrNotFound
		}
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT kind, enabled, params, updated_at FROM alert_rules
		WHERE fleet_id = $1 ORDER BY id`, fleetID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// scanner is the Scan method shared by *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanAlert(s scanner) (Alert, error) {
	var a Alert
	var payload []byte
	var ackedBy sql.NullInt64
	var ackedAt sql.NullTime
	if err := s.Scan(&a.ID, &a.FleetID, &a.Kind, &a.Severity, &payload, &a.RaisedAt, &ackedBy, &ackedAt); err != nil {
		return Alert{}, err
	}
	a.Payload = json.RawMessage(payload)
	if ackedBy.Valid {
		v := ackedBy.Int64
		a.AckedBy = &v
	}
	if ackedAt.Valid {
		v := ackedAt.Time
		a.AckedAt = &v
	}
	return a, nil
}

func scanRule(s scanner) (Rule, error) {
	var r Rule
	var params []byte
	if err := s.Scan(&r.Kind, &r.Enabled, &params, &r.UpdatedAt); err != nil {
		return Rule{}, err
	}
	r.Params = json.RawMessage(params)
	r.Evaluated = r.Kind != KindETASlip
	return r, nil
}
