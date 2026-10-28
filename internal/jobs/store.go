package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// Sentinel errors. The HTTP layer maps each to one apierr code (GF-178).
var (
	ErrNotFound         = errors.New("jobs: not found")
	ErrConflict         = errors.New("jobs: conflict")
	ErrValidation       = errors.New("jobs: validation failed")
	ErrVehicleNotActive = errors.New("jobs: vehicle not active")
)

// Outcome says whether a store call changed a job (D-24).
type Outcome int

const (
	// Applied means the call moved the job and wrote one event.
	Applied Outcome = iota
	// Replayed means the job already had the requested state or payload; nothing was written.
	Replayed
)

// vehicleActive is the registry status an assignment requires (GF-175).
const vehicleActive = "active"

// timeLayout is RFC3339 UTC with millisecond precision and a Z suffix (GF-173).
const timeLayout = "2006-01-02T15:04:05.000Z07:00"

// Point is a WGS84 coordinate in the JSON shape of GF-173.
type Point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// stamp is a timestamp in the GF-173 encoding.
type stamp time.Time

// MarshalJSON writes RFC3339 UTC with milliseconds and a Z suffix.
func (s stamp) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Time(s).UTC().Format(timeLayout))
}

// Job is the GF-173 job object.
type Job struct {
	ID            int64    `json:"id"`
	FleetID       int64    `json:"fleet_id"`
	State         State    `json:"state"`
	VehicleID     *int64   `json:"vehicle_id"`
	DriverID      *int64   `json:"driver_id"`
	Pickup        Point    `json:"pickup"`
	Dropoff       Point    `json:"dropoff"`
	WindowStart   *stamp   `json:"window_start"`
	WindowEnd     *stamp   `json:"window_end"`
	EtaMinutes    *int     `json:"eta_minutes"`
	Notes         *string  `json:"notes"`
	SizeClass     *string  `json:"size_class"`
	WeightKg      *float64 `json:"weight_kg"`
	FailureReason *string  `json:"failure_reason"`
	DeliveredAt   *stamp   `json:"delivered_at"`
	CreatedAt     stamp    `json:"created_at"`
}

// NewJob is a validated create request. Every created job is state=created and unassigned.
type NewJob struct {
	Pickup      Point
	Dropoff     Point
	WindowStart *time.Time
	WindowEnd   *time.Time
	EtaMinutes  *int
	Notes       *string
	SizeClass   *string
	WeightKg    *float64
}

// Change is one applied transition or assignment. At is the job_events.at the trigger wrote.
type Change struct {
	Job  Job
	From State
	To   State
	At   time.Time
}

// Filter narrows GET /jobs (GF-174). Day is a validated YYYY-MM-DD in the fleet timezone.
type Filter struct {
	State    *State
	DriverID *int64
	Day      *string
}

// Store runs job reads and writes. Every mutation opens one transaction whose
// first statement sets app.actor_id (GF-180); the trigger writes job_events.
type Store struct {
	DB *sql.DB
}

// DriverUserID returns the users.id linked to driverID in fleetID, or nil when
// the driver is unset or no user is linked (GF-341). One fleet-scoped read, so
// the hub never needs Postgres (CLAUDE.md, ws-hub).
func (s *Store) DriverUserID(ctx context.Context, fleetID int64, driverID *int64) (*int64, error) {
	if driverID == nil {
		return nil, nil
	}
	var id int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT id FROM users WHERE driver_id = $1 AND fleet_id = $2 ORDER BY id LIMIT 1`,
		*driverID, fleetID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("driver user lookup: %w", err)
	}
	return &id, nil
}

// querier is satisfied by *sql.DB and *sql.Tx.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type rowScanner interface {
	Scan(dest ...any) error
}

// selectJobSQL lists the GF-173 columns. Coordinates come from the geography columns.
const selectJobSQL = `
SELECT j.id, j.fleet_id, j.state, j.vehicle_id, j.driver_id,
       ST_Y(j.pickup::geometry), ST_X(j.pickup::geometry),
       ST_Y(j.dropoff::geometry), ST_X(j.dropoff::geometry),
       j.window_start, j.window_end, j.eta_minutes, j.notes, j.size_class,
       j.weight_kg::float8, j.failure_reason, j.delivered_at, j.created_at
FROM jobs j`

func scanJob(r rowScanner) (Job, error) {
	var (
		j                      Job
		state                  string
		vehicle, driver        sql.Null[int64]
		windowStart, windowEnd sql.Null[time.Time]
		eta                    sql.Null[int64]
		notes, size, failure   sql.Null[string]
		weight                 sql.Null[float64]
		delivered              sql.Null[time.Time]
		created                time.Time
	)
	err := r.Scan(&j.ID, &j.FleetID, &state, &vehicle, &driver,
		&j.Pickup.Lat, &j.Pickup.Lng, &j.Dropoff.Lat, &j.Dropoff.Lng,
		&windowStart, &windowEnd, &eta, &notes, &size, &weight, &failure, &delivered, &created)
	if err != nil {
		return Job{}, err
	}
	j.State = State(state)
	j.VehicleID = optional(vehicle)
	j.DriverID = optional(driver)
	j.WindowStart = optionalStamp(windowStart)
	j.WindowEnd = optionalStamp(windowEnd)
	if v := optional(eta); v != nil {
		n := int(*v)
		j.EtaMinutes = &n
	}
	j.Notes = optional(notes)
	j.SizeClass = optional(size)
	j.WeightKg = optional(weight)
	j.FailureReason = optional(failure)
	j.DeliveredAt = optionalStamp(delivered)
	j.CreatedAt = stamp(created)
	return j, nil
}

// optional maps a SQL null to a nil pointer.
func optional[T any](n sql.Null[T]) *T {
	if !n.Valid {
		return nil
	}
	v := n.V
	return &v
}

func optionalStamp(n sql.Null[time.Time]) *stamp {
	if !n.Valid {
		return nil
	}
	s := stamp(n.V)
	return &s
}

// selectJob reads one job of the fleet. lock adds FOR UPDATE (D-24).
func selectJob(ctx context.Context, q querier, fleetID, id int64, lock bool) (Job, error) {
	query := selectJobSQL + ` WHERE j.id = $1 AND j.fleet_id = $2`
	if lock {
		query += ` FOR UPDATE`
	}
	j, err := scanJob(q.QueryRowContext(ctx, query, id, fleetID))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("read job: %w", err)
	}
	return j, nil
}

// begin opens a transaction and sets app.actor_id as its first statement (GF-180).
func (s *Store) begin(ctx context.Context, actor int64) (*sql.Tx, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin job tx: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('app.actor_id', $1::text, true)`, actor); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("set actor: %w", err)
	}
	return tx, nil
}

// mapWriteErr turns trigger and constraint failures into store sentinels.
// FL428 (actor missing) is not mapped: it is a programming error and returns as a 500.
func mapWriteErr(err error) error {
	if err == nil {
		return nil
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		switch pqErr.Code {
		case "FL409":
			return ErrConflict
		case "23514":
			return ErrValidation
		}
	}
	return err
}

// appliedChange reads the event the trigger wrote in this transaction and the job after it.
func appliedChange(ctx context.Context, tx *sql.Tx, fleetID, jobID int64) (Change, error) {
	var from sql.NullString
	var to string
	var at time.Time
	err := tx.QueryRowContext(ctx,
		`SELECT from_state, to_state, at FROM job_events WHERE job_id = $1 ORDER BY id DESC LIMIT 1`,
		jobID).Scan(&from, &to, &at)
	if err != nil {
		return Change{}, fmt.Errorf("read job event: %w", err)
	}
	job, err := selectJob(ctx, tx, fleetID, jobID, false)
	if err != nil {
		return Change{}, err
	}
	return Change{Job: job, From: State(from.String), To: State(to), At: at}, nil
}

// Create inserts a job in state created with no vehicle or driver. The AFTER
// INSERT trigger writes the creation event (GF-171).
func (s *Store) Create(ctx context.Context, actor, fleetID int64, in NewJob) (Job, error) {
	tx, err := s.begin(ctx, actor)
	if err != nil {
		return Job{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var id int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO jobs (fleet_id, pickup, dropoff, window_start, window_end, eta_minutes, notes, size_class, weight_kg)
		VALUES ($1,
		        ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography,
		        ST_SetSRID(ST_MakePoint($4, $5), 4326)::geography,
		        $6, $7, $8, $9, $10, $11)
		RETURNING id`,
		fleetID, in.Pickup.Lng, in.Pickup.Lat, in.Dropoff.Lng, in.Dropoff.Lat,
		in.WindowStart, in.WindowEnd, in.EtaMinutes, in.Notes, in.SizeClass, in.WeightKg,
	).Scan(&id)
	if err != nil {
		return Job{}, mapWriteErr(err)
	}
	job, err := selectJob(ctx, tx, fleetID, id, false)
	if err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, fmt.Errorf("commit create: %w", err)
	}
	return job, nil
}

// Get reads one job of the fleet. A job of another fleet is ErrNotFound.
func (s *Store) Get(ctx context.Context, fleetID, id int64) (Job, error) {
	return selectJob(ctx, s.DB, fleetID, id, false)
}

// listSQL is GET /jobs (GF-174). A NULL filter parameter matches everything.
// The day filter compares window_start in the fleet timezone, so a NULL window
// never matches a day.
const listSQL = selectJobSQL + `
JOIN fleets fl ON fl.id = j.fleet_id
WHERE j.fleet_id = $1
  AND ($2::text IS NULL OR j.state = $2::text)
  AND ($3::bigint IS NULL OR j.driver_id = $3::bigint)
  AND ($4::date IS NULL OR (j.window_start AT TIME ZONE fl.timezone)::date = $4::date)
ORDER BY j.id ASC`

// List returns the fleet's jobs that match f, by id (GF-174).
func (s *Store) List(ctx context.Context, fleetID int64, f Filter) ([]Job, error) {
	var state *string
	if f.State != nil {
		v := string(*f.State)
		state = &v
	}
	rows, err := s.DB.QueryContext(ctx, listSQL, fleetID, state, f.DriverID, f.Day)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list rows: %w", err)
	}
	return out, nil
}

// Transition moves a job to a state other than assigned (GF-172), per D-24:
// lock the row; the current state equal to the target is a replay with no
// event; a D-23 edge is applied by an UPDATE guarded on the state read under the
// lock; anything else is ErrConflict. The trigger writes the event.
func (s *Store) Transition(ctx context.Context, actor, fleetID, jobID int64, to State) (Change, Outcome, error) {
	if !to.Valid() || to == StateAssigned {
		return Change{}, Applied, ErrValidation
	}
	tx, err := s.begin(ctx, actor)
	if err != nil {
		return Change{}, Applied, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := selectJob(ctx, tx, fleetID, jobID, true)
	if err != nil {
		return Change{}, Applied, err
	}
	if current.State == to {
		return replayed(tx, current)
	}
	if !CanTransition(current.State, to) {
		return Change{}, Applied, ErrConflict
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET state = $1 WHERE id = $2 AND fleet_id = $3 AND state = $4`,
		string(to), jobID, fleetID, string(current.State))
	if err != nil {
		return Change{}, Applied, mapWriteErr(err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return Change{}, Applied, fmt.Errorf("rows affected: %w", err)
	} else if n == 0 {
		// D-24: zero rows, then re-read. The row lock makes this branch
		// unreachable in practice; it keeps the rule as written.
		now, err := selectJob(ctx, tx, fleetID, jobID, false)
		if err != nil {
			return Change{}, Applied, err
		}
		if now.State == to {
			return replayed(tx, now)
		}
		return Change{}, Applied, ErrConflict
	}

	change, err := appliedChange(ctx, tx, fleetID, jobID)
	if err != nil {
		return Change{}, Applied, err
	}
	if err := tx.Commit(); err != nil {
		return Change{}, Applied, fmt.Errorf("commit transition: %w", err)
	}
	return change, Applied, nil
}

// replayed commits the read-only transaction and reports a replay.
func replayed(tx *sql.Tx, j Job) (Change, Outcome, error) {
	if err := tx.Commit(); err != nil {
		return Change{}, Replayed, fmt.Errorf("commit replay: %w", err)
	}
	return Change{Job: j, From: j.State, To: j.State}, Replayed, nil
}

// Assign assigns or reassigns a job to a vehicle and a driver (GF-175). The
// checks run in one transaction in the order GF-175 gives.
func (s *Store) Assign(ctx context.Context, actor, fleetID, jobID, vehicleID, driverID int64) (Change, Outcome, error) {
	tx, err := s.begin(ctx, actor)
	if err != nil {
		return Change{}, Applied, err
	}
	defer func() { _ = tx.Rollback() }()

	// 1. Not in fleet is ErrNotFound; selectJob locks the row.
	job, err := selectJob(ctx, tx, fleetID, jobID, true)
	if err != nil {
		return Change{}, Applied, err
	}
	// 2. Same state and same payload is a replay. Vehicle status is not rechecked.
	if job.State == StateAssigned && samePayload(job, vehicleID, driverID) {
		return replayed(tx, job)
	}
	// 3. Only created and assigned accept an assignment.
	if job.State != StateCreated && job.State != StateAssigned {
		return Change{}, Applied, ErrConflict
	}
	// 4. The vehicle must be in the fleet and active.
	var status string
	err = tx.QueryRowContext(ctx,
		`SELECT status FROM vehicles WHERE id = $1 AND fleet_id = $2`, vehicleID, fleetID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return Change{}, Applied, ErrNotFound
	}
	if err != nil {
		return Change{}, Applied, fmt.Errorf("check vehicle: %w", err)
	}
	if status != vehicleActive {
		return Change{}, Applied, ErrVehicleNotActive
	}
	// 5. The driver must be in the fleet. Drivers have no status column.
	var one int
	err = tx.QueryRowContext(ctx,
		`SELECT 1 FROM drivers WHERE id = $1 AND fleet_id = $2`, driverID, fleetID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return Change{}, Applied, ErrNotFound
	}
	if err != nil {
		return Change{}, Applied, fmt.Errorf("check driver: %w", err)
	}

	// 6. created -> assigned, or assigned -> assigned (reassign). Zero rows is a lost race.
	var res sql.Result
	if job.State == StateCreated {
		res, err = tx.ExecContext(ctx, `
			UPDATE jobs SET vehicle_id = $1, driver_id = $2, state = 'assigned'
			WHERE id = $3 AND fleet_id = $4 AND state = 'created'`,
			vehicleID, driverID, jobID, fleetID)
	} else {
		res, err = tx.ExecContext(ctx, `
			UPDATE jobs SET vehicle_id = $1, driver_id = $2
			WHERE id = $3 AND fleet_id = $4 AND state = 'assigned'`,
			vehicleID, driverID, jobID, fleetID)
	}
	if err != nil {
		return Change{}, Applied, mapWriteErr(err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return Change{}, Applied, fmt.Errorf("rows affected: %w", err)
	} else if n == 0 {
		return Change{}, Applied, ErrConflict
	}

	change, err := appliedChange(ctx, tx, fleetID, jobID)
	if err != nil {
		return Change{}, Applied, err
	}
	if err := tx.Commit(); err != nil {
		return Change{}, Applied, fmt.Errorf("commit assign: %w", err)
	}
	return change, Applied, nil
}

func samePayload(j Job, vehicleID, driverID int64) bool {
	return j.VehicleID != nil && *j.VehicleID == vehicleID && j.DriverID != nil && *j.DriverID == driverID
}

// Cancel moves a job to cancelled from created, assigned or accepted (GF-176).
// A job already cancelled is a replay.
func (s *Store) Cancel(ctx context.Context, actor, fleetID, jobID int64) (Change, Outcome, error) {
	return s.Transition(ctx, actor, fleetID, jobID, StateCancelled)
}
