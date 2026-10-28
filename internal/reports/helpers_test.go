package reports

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // the fixtures load America/New_York on hosts without zoneinfo

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"fleetops/internal/ingest"
	"fleetops/internal/testinfra"
)

// earthR is the sphere radius PostGIS ST_DistanceSphere uses (D-32, Testing depth).
const earthR = 6371008.8

// haversineM is the Go reference for ST_DistanceSphere, in metres.
func haversineM(lat1, lng1, lat2, lng2 float64) float64 {
	const rad = math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLng := (lng2 - lng1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthR * math.Asin(math.Min(1, math.Sqrt(a)))
}

// civil returns the UTC midnight of a civil date, the form RollupDay takes.
func civil(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// pos is one position sample as the fixtures insert it.
type pos struct {
	fleet, vehicle int64
	ts             time.Time
	lat, lng       float64
	speed          float64
}

// stat is one daily_driver_stats row as the tests read it back.
type stat struct {
	DriverID      int64
	JobsCompleted int
	OnTimeCount   int
	OnTimeDenom   int
	DistanceM     float64
	IdleS         int
}

// fx is one migrated database with helpers for the rollup tests.
type fx struct {
	t  *testing.T
	db *sql.DB
	m  *Metrics
}

// newFx migrates a fresh database and creates the monthly partitions for March 2026,
// the only month the fixtures use.
func newFx(t *testing.T) *fx {
	t.Helper()
	db := testinfra.NewMigratedDB(t)
	if err := ingest.EnsurePartitions(context.Background(), db, civil(2026, time.March, 1)); err != nil {
		t.Fatalf("partitions: %v", err)
	}
	return &fx{t: t, db: db, m: NewMetrics(prometheus.NewRegistry())}
}

func (f *fx) fleet(name, tz string) int64 {
	f.t.Helper()
	var id int64
	if err := f.db.QueryRow(`INSERT INTO fleets (name, timezone) VALUES ($1, $2) RETURNING id`, name, tz).Scan(&id); err != nil {
		f.t.Fatalf("insert fleet: %v", err)
	}
	return id
}

func (f *fx) driver(fleet int64, name string) int64 {
	f.t.Helper()
	var id int64
	// license_no_hash is NOT NULL and unique per fleet, so each driver gets its own bytes.
	lic := []byte(fmt.Sprintf("lic-%s-%d", name, time.Now().UnixNano()))
	if err := f.db.QueryRow(
		`INSERT INTO drivers (fleet_id, name, license_no_hash) VALUES ($1, $2, $3) RETURNING id`,
		fleet, name, lic,
	).Scan(&id); err != nil {
		f.t.Fatalf("insert driver: %v", err)
	}
	return id
}

func (f *fx) vehicle(fleet int64) int64 {
	f.t.Helper()
	var id int64
	if err := f.db.QueryRow(
		`INSERT INTO vehicles (fleet_id, plate, status) VALUES ($1, $2, 'active') RETURNING id`,
		fleet, fmt.Sprintf("R-%d", time.Now().UnixNano()),
	).Scan(&id); err != nil {
		f.t.Fatalf("insert vehicle: %v", err)
	}
	return id
}

// pair opens or closes a pairing of the vehicle with driver (0 means driverless).
// Each pairing gets its own device, so the open-device unique index holds.
func (f *fx) pair(fleet, vehicle, driver int64, started time.Time, ended *time.Time) {
	f.t.Helper()
	var dev int64
	if err := f.db.QueryRow(
		`INSERT INTO devices (fleet_id, kind, api_key_hash) VALUES ($1, 'tracker', '\x00') RETURNING id`, fleet,
	).Scan(&dev); err != nil {
		f.t.Fatalf("insert device: %v", err)
	}
	var drv any
	if driver != 0 {
		drv = driver
	}
	if _, err := f.db.Exec(`
		INSERT INTO vehicle_pairings (fleet_id, vehicle_id, driver_id, device_id, started_at, ended_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, fleet, vehicle, drv, dev, started, ended); err != nil {
		f.t.Fatalf("insert pairing: %v", err)
	}
}

// job inserts a job. A nil delivered leaves it undelivered; a nil window leaves it unwindowed.
func (f *fx) job(fleet int64, state string, vehicle, driver int64, win *[2]time.Time, delivered *time.Time) {
	f.t.Helper()
	var ws, we any
	if win != nil {
		ws, we = win[0], win[1]
	}
	var dt any
	if delivered != nil {
		dt = *delivered
	}
	// The jobs trigger writes job_events and needs app.actor_id in the same transaction.
	tx, err := f.db.Begin()
	if err != nil {
		f.t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SELECT set_config('app.actor_id', $1::text, true)`, f.user(fleet)); err != nil {
		f.t.Fatalf("set actor: %v", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO jobs (fleet_id, state, vehicle_id, driver_id, pickup, dropoff, window_start, window_end, delivered_at)
		VALUES ($1, $2, $3, $4, ST_SetSRID(ST_MakePoint(13.4, 52.5), 4326)::geography,
		        ST_SetSRID(ST_MakePoint(13.5, 52.6), 4326)::geography, $5, $6, $7)`,
		fleet, state, vehicle, driver, ws, we, dt); err != nil {
		f.t.Fatalf("insert job: %v", err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatalf("commit job: %v", err)
	}
}

// user inserts an admin user in the fleet to act as the job event actor.
func (f *fx) user(fleet int64) int64 {
	f.t.Helper()
	var id int64
	email := fmt.Sprintf("actor-%d@reports.example", time.Now().UnixNano())
	if err := f.db.QueryRow(
		`INSERT INTO users (fleet_id, email, password_hash, role) VALUES ($1, $2, 'x', 'admin') RETURNING id`,
		fleet, email,
	).Scan(&id); err != nil {
		f.t.Fatalf("insert user: %v", err)
	}
	return id
}

// positions inserts samples in multi-row batches of 500. device_ts equals server_ts.
func (f *fx) positions(ps []pos) {
	f.t.Helper()
	for start := 0; start < len(ps); start += 500 {
		end := min(start+500, len(ps))
		var b strings.Builder
		b.WriteString(`INSERT INTO positions (msg_id, fleet_id, vehicle_id, geom, speed_kmh, heading_deg, accuracy_m, device_ts, server_ts) VALUES `)
		args := make([]any, 0, (end-start)*7)
		for i, p := range ps[start:end] {
			if i > 0 {
				b.WriteString(",")
			}
			n := len(args)
			fmt.Fprintf(&b, "(gen_random_uuid(), $%d, $%d, ST_SetSRID(ST_MakePoint($%d, $%d), 4326)::geography, $%d, 0, 5, $%d, $%d)",
				n+1, n+2, n+3, n+4, n+5, n+6, n+6)
			args = append(args, p.fleet, p.vehicle, p.lng, p.lat, p.speed, p.ts)
		}
		if _, err := f.db.Exec(b.String(), args...); err != nil {
			f.t.Fatalf("insert positions: %v", err)
		}
	}
}

// rollup runs RollupDay in its own transaction and returns the write counts.
func (f *fx) rollup(fleet int64, loc *time.Location, day time.Time) Written {
	f.t.Helper()
	tx, err := f.db.BeginTx(context.Background(), nil)
	if err != nil {
		f.t.Fatalf("begin: %v", err)
	}
	w, err := RollupDay(context.Background(), tx, fleet, loc, day)
	if err != nil {
		_ = tx.Rollback()
		f.t.Fatalf("RollupDay: %v", err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatalf("commit: %v", err)
	}
	return w
}

// stats reads the rollup rows of one fleet-day, by driver id.
func (f *fx) stats(fleet int64, day time.Time) []stat {
	f.t.Helper()
	rows, err := f.db.Query(`
		SELECT driver_id, jobs_completed, on_time_count, on_time_denom, distance_m, idle_s
		FROM daily_driver_stats WHERE fleet_id = $1 AND day = $2 ORDER BY driver_id`, fleet, day.Format(time.DateOnly))
	if err != nil {
		f.t.Fatalf("read stats: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []stat
	for rows.Next() {
		var s stat
		if err := rows.Scan(&s.DriverID, &s.JobsCompleted, &s.OnTimeCount, &s.OnTimeDenom, &s.DistanceM, &s.IdleS); err != nil {
			f.t.Fatalf("scan stats: %v", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		f.t.Fatalf("stats rows: %v", err)
	}
	return out
}

// skippedCount reads fleetops_reports_rollup_passes_skipped_total{reason}.
func skippedCount(t *testing.T, m *Metrics, reason string) float64 {
	t.Helper()
	var out dto.Metric
	if err := m.PassesSkipped.WithLabelValues(reason).Write(&out); err != nil {
		t.Fatalf("read skipped: %v", err)
	}
	return out.GetCounter().GetValue()
}

// at returns the UTC time of a 2026-03-08 clock reading.
func at(hh, mm, ss int) time.Time {
	return time.Date(2026, time.March, 8, hh, mm, ss, 0, time.UTC)
}
