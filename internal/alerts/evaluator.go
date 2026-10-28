package alerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"fleetops/internal/geofence"
)

// Interval is the evaluator tick (GF-291).
const Interval = 30 * time.Second

// passLockKey is the fixed advisory-lock key. Only one pass runs across replicas (GF-291).
const passLockKey int64 = 0x46_41_4c_45 // "FALE"

// Raised is one alert inserted by a pass. The publish and notify steps read it after commit.
type Raised struct {
	ID       int64
	FleetID  int64
	Kind     string
	Severity string
	Payload  json.RawMessage
	RaisedAt time.Time
}

// Evaluator runs the device_silent and geofence_exit_outside_schedule passes
// (GF-291). It lives in services/ingest, beside the presence sweeper, and is
// not a flush hook: an alert error never changes a position ack.
type Evaluator struct {
	DB      *sql.DB
	Clock   func() time.Time
	Metrics *Metrics
}

// Run runs one pass per interval until ctx is done. A pass error is logged and
// counted; the next tick tries again.
func (e *Evaluator) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := e.Pass(ctx); err != nil && ctx.Err() == nil {
				log.Printf("alerts: pass: %v", err)
			}
		}
	}
}

func (e *Evaluator) now() time.Time {
	if e.Clock != nil {
		return e.Clock()
	}
	return time.Now()
}

// rule is one loaded alert_rules row (GF-297).
type rule struct {
	FleetID    int64
	Kind       string
	Enabled    bool
	ThresholdS int
}

// Pass runs one evaluation in one transaction (GF-294): take the advisory lock,
// load the rules, find candidates, insert, commit. A pass that cannot take the
// lock does nothing and returns no alerts (GF-291). The returned alerts are
// committed.
func (e *Evaluator) Pass(ctx context.Context) ([]Raised, error) {
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		e.Metrics.fail(stageRules)
		return nil, fmt.Errorf("begin pass: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, passLockKey).Scan(&locked); err != nil {
		e.Metrics.fail(stageRules)
		return nil, fmt.Errorf("advisory lock: %w", err)
	}
	if !locked {
		if e.Metrics != nil {
			e.Metrics.PassesSkipped.Inc()
		}
		return nil, nil
	}
	start := time.Now()
	now := e.now()

	rules, err := loadRules(ctx, tx)
	if err != nil {
		e.Metrics.fail(stageRules)
		return nil, fmt.Errorf("load rules: %w", err)
	}

	var raised []Raised
	exitEnabled := false
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		switch r.Kind {
		case KindDeviceSilent:
			got, err := e.silent(ctx, tx, r, now)
			if err != nil {
				return nil, err
			}
			raised = append(raised, got...)
		case KindGeofenceExit:
			exitEnabled = true
		}
		// eta_slip has no evaluator in P19 (GF-290).
	}
	if exitEnabled {
		got, err := e.exits(ctx, tx, now)
		if err != nil {
			return nil, err
		}
		raised = append(raised, got...)
	}

	if err := tx.Commit(); err != nil {
		e.Metrics.fail(stageInsert)
		return nil, fmt.Errorf("commit pass: %w", err)
	}
	if e.Metrics != nil {
		e.Metrics.EvalDuration.Observe(time.Since(start).Seconds())
		for _, a := range raised {
			e.Metrics.Raised.WithLabelValues(a.Kind, a.Severity).Inc()
		}
	}
	return raised, nil
}

// loadRules reads every rule row in one query (GF-297). No cache.
func loadRules(ctx context.Context, tx *sql.Tx) ([]rule, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT fleet_id, kind, enabled, COALESCE((params->>'threshold_s')::int, 0)
		FROM alert_rules ORDER BY fleet_id, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []rule
	for rows.Next() {
		var r rule
		if err := rows.Scan(&r.FleetID, &r.Kind, &r.Enabled, &r.ThresholdS); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// silencePayload is the device_silent payload (GF-292).
type silencePayload struct {
	VehicleID  int64  `json:"vehicle_id"`
	LastSeenAt string `json:"last_seen_at"`
	SilentForS int64  `json:"silent_for_s"`
}

// silent raises device_silent alerts for one fleet's rule (GF-292). The
// candidate query applies the status, pairing and threshold filters and the
// episode NOT EXISTS. decideSilence repeats the decision in Go.
func (e *Evaluator) silent(ctx context.Context, tx *sql.Tx, r rule, now time.Time) ([]Raised, error) {
	threshold := time.Duration(r.ThresholdS) * time.Second
	rows, err := tx.QueryContext(ctx, `
		SELECT vl.vehicle_id, vl.server_ts
		FROM vehicles_latest vl
		JOIN vehicles v ON v.id = vl.vehicle_id
		WHERE vl.fleet_id = $1
		  AND v.status = 'active'
		  AND v.paired_device_id IS NOT NULL
		  AND vl.server_ts < $2::timestamptz - make_interval(secs => $3::int)
		  AND NOT EXISTS (
		    SELECT 1 FROM alerts a
		    WHERE a.fleet_id = vl.fleet_id
		      AND a.kind = 'device_silent'
		      AND (a.payload->>'vehicle_id')::bigint = vl.vehicle_id
		      AND (a.payload->>'last_seen_at')::timestamptz = vl.server_ts)
		ORDER BY vl.vehicle_id`, r.FleetID, now, r.ThresholdS)
	if err != nil {
		e.Metrics.fail(stageCandidates)
		return nil, fmt.Errorf("device_silent candidates: %w", err)
	}
	type candidate struct {
		vehicleID int64
		lastSeen  time.Time
	}
	var cands []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.vehicleID, &c.lastSeen); err != nil {
			_ = rows.Close()
			e.Metrics.fail(stageCandidates)
			return nil, err
		}
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		e.Metrics.fail(stageCandidates)
		return nil, err
	}
	_ = rows.Close()

	var out []Raised
	for _, c := range cands {
		if !decideSilence(c.lastSeen, now, threshold, false) {
			continue
		}
		payload, err := json.Marshal(silencePayload{
			VehicleID:  c.vehicleID,
			LastSeenAt: c.lastSeen.UTC().Format(time.RFC3339Nano),
			SilentForS: int64(now.Sub(c.lastSeen) / time.Second),
		})
		if err != nil {
			return nil, err
		}
		a, err := insertAlert(ctx, tx, r.FleetID, KindDeviceSilent, SeverityWarning, payload, now)
		if err != nil {
			e.Metrics.fail(stageInsert)
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// exitPayload is the geofence_exit_outside_schedule payload (GF-293). No coordinates.
type exitPayload struct {
	EventID    int64  `json:"event_id"`
	GeofenceID int64  `json:"geofence_id"`
	VehicleID  int64  `json:"vehicle_id"`
	OccurredAt string `json:"occurred_at"`
}

// exits scans the last 24 h of exit events for fences with a schedule and an
// enabled rule. An exit outside the window is critical (GF-293, GF-296).
func (e *Evaluator) exits(ctx context.Context, tx *sql.Tx, now time.Time) ([]Raised, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT ge.id, ge.fleet_id, ge.geofence_id, ge.vehicle_id, ge.occurred_at, g.schedule
		FROM geofence_events ge
		JOIN geofences g ON g.id = ge.geofence_id
		WHERE ge.kind = 'exit'
		  AND ge.created_at > $1::timestamptz - interval '24 hours'
		  AND g.schedule IS NOT NULL
		  AND EXISTS (
		    SELECT 1 FROM alert_rules r
		    WHERE r.fleet_id = ge.fleet_id
		      AND r.kind = 'geofence_exit_outside_schedule'
		      AND r.enabled)
		  AND NOT EXISTS (
		    SELECT 1 FROM alerts a
		    WHERE a.fleet_id = ge.fleet_id
		      AND a.kind = 'geofence_exit_outside_schedule'
		      AND (a.payload->>'event_id')::bigint = ge.id)
		ORDER BY ge.id`, now)
	if err != nil {
		e.Metrics.fail(stageCandidates)
		return nil, fmt.Errorf("exit candidates: %w", err)
	}
	type candidate struct {
		eventID, fleetID, geofenceID, vehicleID int64
		occurred                                time.Time
		schedule                                []byte
	}
	var cands []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.eventID, &c.fleetID, &c.geofenceID, &c.vehicleID, &c.occurred, &c.schedule); err != nil {
			_ = rows.Close()
			e.Metrics.fail(stageCandidates)
			return nil, err
		}
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		e.Metrics.fail(stageCandidates)
		return nil, err
	}
	_ = rows.Close()

	var out []Raised
	for _, c := range cands {
		var sched geofence.Schedule
		if err := json.Unmarshal(c.schedule, &sched); err != nil {
			e.Metrics.fail(stageCandidates)
			return nil, fmt.Errorf("geofence %d schedule: %w", c.geofenceID, err)
		}
		if InSchedule(&sched, c.occurred) {
			continue
		}
		payload, err := json.Marshal(exitPayload{
			EventID:    c.eventID,
			GeofenceID: c.geofenceID,
			VehicleID:  c.vehicleID,
			OccurredAt: c.occurred.UTC().Format(time.RFC3339Nano),
		})
		if err != nil {
			return nil, err
		}
		a, err := insertAlert(ctx, tx, c.fleetID, KindGeofenceExit, SeverityCritical, payload, now)
		if err != nil {
			e.Metrics.fail(stageInsert)
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// insertAlert writes one alerts row inside the pass transaction. raised_at is the pass time (GF-291).
func insertAlert(ctx context.Context, tx *sql.Tx, fleetID int64, kind, severity string, payload json.RawMessage, at time.Time) (Raised, error) {
	var id int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO alerts (fleet_id, kind, severity, payload, raised_at)
		VALUES ($1, $2, $3, $4::jsonb, $5)
		RETURNING id`, fleetID, kind, severity, string(payload), at,
	).Scan(&id); err != nil {
		return Raised{}, fmt.Errorf("insert %s alert: %w", kind, err)
	}
	return Raised{ID: id, FleetID: fleetID, Kind: kind, Severity: severity, Payload: payload, RaisedAt: at}, nil
}
