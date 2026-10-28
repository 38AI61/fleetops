package eta

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/lib/pq"

	"fleetops/internal/alerts"
)

// Interval is the loop tick (GF-306). It bounds assign latency; the 5-min limit is GF-307.
const Interval = 30 * time.Second

// passLockKey is the ETA advisory-lock key, "ETAL". Only one pass runs across replicas (GF-306).
const passLockKey int64 = 0x45_54_41_4c

// activeStates are the states that still need an ETA (D-35).
var activeStates = []string{"assigned", "accepted", "en_route", "picked_up"}

// Loop recomputes ETAs for active jobs (D-35). It lives in services/ingest beside the
// alert evaluator. Every pass runs in one transaction under passLockKey (GF-306).
type Loop struct {
	DB      *sql.DB
	Router  Router
	Limiter *Limiter
	Clock   func() time.Time
	Metrics *Metrics
	// Publisher sends alert.raised after the pass commits. Nil skips the publish (GF-299).
	// The eta_slip warning is not critical, so no Notifier runs here (GF-300).
	Publisher alerts.Publisher
}

// Run runs one pass per Interval until ctx is done. A pass error is logged; the next tick tries again.
func (l *Loop) Run(ctx context.Context) {
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := l.Pass(ctx); err != nil && ctx.Err() == nil {
				log.Printf("eta: pass: %v", err)
			}
		}
	}
}

func (l *Loop) now() time.Time {
	if l.Clock != nil {
		return l.Clock()
	}
	return time.Now()
}

// candidate is one active job with the points the route call needs.
type candidate struct {
	id          int64
	fleetID     int64
	vehicleID   int64
	windowEnd   *time.Time
	hasPosition bool
	from        Point
	hasTarget   bool
	to          Point
}

// Pass runs one ETA pass. A pass that cannot take the lock does nothing and is counted (GF-306).
// OSRM failures skip the job and never fail the pass (GF-314).
func (l *Loop) Pass(ctx context.Context) error {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin eta pass: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, passLockKey).Scan(&locked); err != nil {
		return fmt.Errorf("eta advisory lock: %w", err)
	}
	if !locked {
		l.Metrics.passSkipped()
		return nil
	}
	start := time.Now()
	now := l.now()

	cands, err := loadCandidates(ctx, tx)
	if err != nil {
		return fmt.Errorf("load eta candidates: %w", err)
	}

	active := make(map[int64]struct{}, len(cands))
	for _, c := range cands {
		active[c.id] = struct{}{}
	}
	l.Limiter.Prune(active)

	slipOn, err := loadSlipRules(ctx, tx)
	if err != nil {
		return fmt.Errorf("load eta_slip rules: %w", err)
	}
	var raised []alerts.Raised

	osrmSkipped := 0
	for _, c := range cands {
		if !c.hasPosition {
			l.Metrics.skip(reasonNoPosition)
			continue
		}
		if !c.hasTarget {
			l.Metrics.skip(reasonNoTarget)
			continue
		}
		if !l.Limiter.Allow(c.id, now) {
			continue
		}

		minutes, err := l.Router.Route(ctx, c.from, c.to)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			outcome := outcomeOf(err)
			l.Metrics.call(outcome)
			l.Metrics.skip(reasonOSRMUnavailable)
			osrmSkipped++
			continue
		}
		l.Metrics.call(OutcomeOK)

		// Clamp again at the write (GF-309). The router already clamps, so this is a guard.
		if _, err := tx.ExecContext(ctx,
			`UPDATE jobs SET eta_minutes = $2 WHERE id = $1 AND eta_minutes IS DISTINCT FROM $2`,
			c.id, clampETA(minutes)); err != nil {
			return fmt.Errorf("write eta for job %d: %w", c.id, err)
		}

		// The slip check runs only on this fresh value and only when the fleet's rule is enabled (GF-310, GF-313).
		if slipOn[c.fleetID] {
			a, err := raiseSlip(ctx, tx, c, clampETA(minutes), now)
			if err != nil {
				return err
			}
			if a != nil {
				raised = append(raised, *a)
			}
		}
	}
	if osrmSkipped > 0 {
		// One line per pass, not per job (GF-314).
		log.Printf("eta: pass skipped %d job(s): OSRM unavailable", osrmSkipped)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit eta pass: %w", err)
	}
	if l.Metrics != nil {
		l.Metrics.PassDuration.Observe(time.Since(start).Seconds())
	}
	// Publish after commit only, as the evaluator does (GF-299).
	if l.Publisher != nil && len(raised) > 0 {
		alerts.PublishRaised(l.Publisher, nil, raised)
	}
	return nil
}

// loadCandidates reads every active job that has a vehicle, with its origin and target (D-35, GF-316).
// The origin is the vehicle's latest position. The target is pickup for assigned and accepted, else dropoff.
func loadCandidates(ctx context.Context, tx *sql.Tx) ([]candidate, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT j.id, j.fleet_id, j.vehicle_id, j.window_end,
		       ST_Y(vl.geom::geometry), ST_X(vl.geom::geometry),
		       ST_Y((CASE WHEN j.state IN ('assigned','accepted') THEN j.pickup ELSE j.dropoff END)::geometry),
		       ST_X((CASE WHEN j.state IN ('assigned','accepted') THEN j.pickup ELSE j.dropoff END)::geometry)
		FROM jobs j
		LEFT JOIN vehicles_latest vl ON vl.vehicle_id = j.vehicle_id
		WHERE j.state = ANY($1) AND j.vehicle_id IS NOT NULL
		ORDER BY j.id`, pq.Array(activeStates))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []candidate
	for rows.Next() {
		var (
			c                candidate
			windowEnd        sql.NullTime
			fromLat, fromLng sql.NullFloat64
			toLat, toLng     sql.NullFloat64
		)
		if err := rows.Scan(&c.id, &c.fleetID, &c.vehicleID, &windowEnd, &fromLat, &fromLng, &toLat, &toLng); err != nil {
			return nil, err
		}
		if windowEnd.Valid {
			we := windowEnd.Time
			c.windowEnd = &we
		}
		c.hasPosition = fromLat.Valid && fromLng.Valid
		c.from = Point{Lat: fromLat.Float64, Lng: fromLng.Float64}
		c.hasTarget = toLat.Valid && toLng.Valid
		c.to = Point{Lat: toLat.Float64, Lng: toLng.Float64}
		out = append(out, c)
	}
	return out, rows.Err()
}

// outcomeOf returns the taxonomy label of a failed route call (GF-304).
func outcomeOf(err error) Outcome {
	var re *RouteError
	if errors.As(err, &re) {
		return re.Outcome
	}
	return OutcomeUnreachable
}

// clampETA bounds a minute count to the jobs.eta_minutes CHECK range (GF-304, GF-309).
func clampETA(minutes int) int {
	switch {
	case minutes < 0:
		return 0
	case minutes > MaxETAMinutes:
		return MaxETAMinutes
	}
	return minutes
}
