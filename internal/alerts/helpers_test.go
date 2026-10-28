package alerts

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lib/pq"
)

// sqlState returns the SQLSTATE of a Postgres error, or "" for anything else.
func sqlState(err error) string {
	var pe *pq.Error
	if errors.As(err, &pe) {
		return string(pe.Code)
	}
	return ""
}

// newFleet inserts a fleet with the UTC zone and returns its id.
func newFleet(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`INSERT INTO fleets (name, timezone) VALUES ($1, 'UTC') RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert fleet: %v", err)
	}
	return id
}

// newUser inserts a user with the role in the fleet and returns its id.
func newUser(t *testing.T, db *sql.DB, fleet int64, role string) int64 {
	t.Helper()
	var id int64
	email := fmt.Sprintf("%s-%d-%d@alerts.example", role, fleet, time.Now().UnixNano())
	if err := db.QueryRow(
		`INSERT INTO users (fleet_id, email, password_hash, role) VALUES ($1, $2, 'x', $3) RETURNING id`,
		fleet, email, role,
	).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

// newAlert inserts an unacked alert straight into SQL and returns its id.
func newAlert(t *testing.T, db *sql.DB, fleet int64, kind, severity string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(
		`INSERT INTO alerts (fleet_id, kind, severity, payload) VALUES ($1, $2, $3, '{}'::jsonb) RETURNING id`,
		fleet, kind, severity,
	).Scan(&id); err != nil {
		t.Fatalf("insert alert: %v", err)
	}
	return id
}
