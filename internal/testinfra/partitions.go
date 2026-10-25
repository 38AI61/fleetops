package testinfra

import (
	"database/sql"
	"fmt"
	"testing"
	"time"
)

// EnsurePositionsPartitions creates positions_YYYY_MM for every UTC calendar
// month from `from` through `to` inclusive (GF-81). Test-only: production never
// calls it, and runtime ensure-ahead belongs to P6.
func EnsurePositionsPartitions(t testing.TB, db *sql.DB, from, to time.Time) {
	t.Helper()
	start := time.Date(from.UTC().Year(), from.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	last := time.Date(to.UTC().Year(), to.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	for m := start; !m.After(last); m = m.AddDate(0, 1, 0) {
		next := m.AddDate(0, 1, 0)
		// Bounds are formatted from time.Time values, never from input; DDL takes no bind parameters.
		stmt := fmt.Sprintf( //nolint:gosec // see comment above
			`CREATE TABLE IF NOT EXISTS positions_%04d_%02d PARTITION OF positions FOR VALUES FROM ('%s') TO ('%s')`,
			m.Year(), int(m.Month()),
			m.Format("2006-01-02 15:04:05+00"), next.Format("2006-01-02 15:04:05+00"),
		)
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create partition for %s: %v", m.Format("2006-01"), err)
		}
	}
}
