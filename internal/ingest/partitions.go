package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"time"

	"github.com/lib/pq"
)

// partitionLockKey serialises ensure-ahead across instances (GF-91). It is an
// arbitrary fixed key, not derived from input.
const partitionLockKey int64 = 0x464c5450

// retainMonths is the retention window in months (D-14, GF-92).
const retainMonths = 12

// maintenanceInterval is the daily cadence after the boot pass (GF-91, GF-92).
const maintenanceInterval = 24 * time.Hour

// partitionNameRE matches the children this package creates and may drop (GF-92).
var partitionNameRE = regexp.MustCompile(`^positions_(\d{4})_(\d{2})$`)

// child is one positions partition with the UTC month it holds.
type child struct {
	name  string
	month time.Time
}

// monthStart is the first instant of the UTC month containing t.
func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// partitionName is positions_YYYY_MM for the month that starts at m.
func partitionName(m time.Time) string {
	return fmt.Sprintf("positions_%04d_%02d", m.Year(), int(m.Month()))
}

// ensureMonths returns the UTC month starts of now, now+1 month, and now+2 months (GF-91).
func ensureMonths(now time.Time) []time.Time {
	first := monthStart(now)
	return []time.Time{first, first.AddDate(0, 1, 0), first.AddDate(0, 2, 0)}
}

// retainCutoff is the start of the UTC month containing now − 12 months (GF-92).
func retainCutoff(now time.Time) time.Time {
	return monthStart(now).AddDate(0, -retainMonths, 0)
}

// retainable returns the children whose upper bound (the first instant of the
// following month) is at or before cutoff (GF-92).
func retainable(children []child, cutoff time.Time) []child {
	var out []child
	for _, c := range children {
		if !c.month.AddDate(0, 1, 0).After(cutoff) {
			out = append(out, c)
		}
	}
	return out
}

// EnsurePartitions creates the current and next two monthly children of
// positions, if missing, in one transaction under the advisory lock (GF-91).
func EnsurePartitions(ctx context.Context, db *sql.DB, now time.Time) error {
	_, err := ensurePartitions(ctx, db, now)
	return err
}

// ensurePartitions is EnsurePartitions plus the number of children it created.
// The count is returned only after COMMIT, so a rolled-back pass reports zero.
func ensurePartitions(ctx context.Context, db *sql.DB, now time.Time) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin ensure partitions: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, partitionLockKey); err != nil {
		return 0, fmt.Errorf("lock ensure partitions: %w", err)
	}

	created := 0
	for _, m := range ensureMonths(now) {
		name := partitionName(m)
		var existing sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT to_regclass($1)::text`, name).Scan(&existing); err != nil {
			return 0, fmt.Errorf("check partition %s: %w", name, err)
		}
		if !existing.Valid {
			created++
		}
		// The bounds come from time.Time, never from input; DDL takes no bind parameters.
		stmt := fmt.Sprintf( //nolint:gosec // see comment above
			`CREATE TABLE IF NOT EXISTS %s PARTITION OF positions FOR VALUES FROM ('%s') TO ('%s')`,
			pq.QuoteIdentifier(name),
			m.Format("2006-01-02 15:04:05+00"), m.AddDate(0, 1, 0).Format("2006-01-02 15:04:05+00"),
		)
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return 0, fmt.Errorf("create partition %s: %w", name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit ensure partitions: %w", err)
	}
	committed = true
	return created, nil
}

// RetainPartitions detaches and drops every child whose upper bound is at or
// before the retention cutoff (GF-92). Each child is dropped in its own
// transaction. It returns how many were dropped before any error.
func RetainPartitions(ctx context.Context, db *sql.DB, now time.Time) (int, error) {
	children, err := listChildren(ctx, db)
	if err != nil {
		return 0, err
	}
	current := monthStart(now)
	dropped := 0
	for _, c := range retainable(children, retainCutoff(now)) {
		// Current and later months are never dropped, whatever the cutoff arithmetic says.
		if !c.month.Before(current) {
			continue
		}
		if err := dropChild(ctx, db, c.name); err != nil {
			return dropped, err
		}
		dropped++
	}
	return dropped, nil
}

// listChildren returns the positions children whose names match partitionNameRE.
// Other children are left alone.
func listChildren(ctx context.Context, db *sql.DB) ([]child, error) {
	rows, err := db.QueryContext(ctx, `
SELECT c.relname
FROM pg_inherits i
JOIN pg_class c ON c.oid = i.inhrelid
WHERE i.inhparent = 'positions'::regclass
ORDER BY c.relname`)
	if err != nil {
		return nil, fmt.Errorf("list partitions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []child
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan partition: %w", err)
		}
		m := partitionNameRE.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		year, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		if month < 1 || month > 12 {
			continue
		}
		out = append(out, child{name: name, month: time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list partitions: %w", err)
	}
	return out, nil
}

// dropChild detaches and drops one child in one transaction; a failure rolls back.
func dropChild(ctx context.Context, db *sql.DB, name string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin drop %s: %w", name, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	q := pq.QuoteIdentifier(name)
	if _, err := tx.ExecContext(ctx, `ALTER TABLE positions DETACH PARTITION `+q); err != nil {
		return fmt.Errorf("detach %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE `+q); err != nil {
		return fmt.Errorf("drop %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit drop %s: %w", name, err)
	}
	committed = true
	return nil
}

// Maintenance runs the partition lifecycle: ensure-ahead, then retention (GF-91, GF-92).
type Maintenance struct {
	DB      *sql.DB
	Clock   func() time.Time
	Metrics *Metrics
}

// RunOnce runs one pass at now: ensure, then retain. A failed ensure does not
// skip retention. Both errors are joined and counted per op (GF-93).
func (m *Maintenance) RunOnce(ctx context.Context, now time.Time) error {
	created, ensureErr := ensurePartitions(ctx, m.DB, now)
	m.Metrics.PartitionsCreated.Add(float64(created))
	if ensureErr != nil {
		m.Metrics.PartitionMaintenanceErrors.WithLabelValues("ensure").Inc()
	}

	dropped, retainErr := RetainPartitions(ctx, m.DB, now)
	m.Metrics.PartitionsDropped.Add(float64(dropped))
	if retainErr != nil {
		m.Metrics.PartitionMaintenanceErrors.WithLabelValues("retain").Inc()
	}
	return errors.Join(ensureErr, retainErr)
}

// Run makes the boot pass (ensure, then retain), calls ready when it returns,
// and then repeats the pass every 24 h until ctx is done. Errors are logged;
// the failure counters record them. ready may be nil.
func (m *Maintenance) Run(ctx context.Context, ready func()) {
	if err := m.RunOnce(ctx, m.Clock()); err != nil {
		log.Printf("ingest: partition maintenance: %v", err)
	}
	if ready != nil {
		ready()
	}

	ticker := time.NewTicker(maintenanceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.RunOnce(ctx, m.Clock()); err != nil {
				log.Printf("ingest: partition maintenance: %v", err)
			}
		}
	}
}
