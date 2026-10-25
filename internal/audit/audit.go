// Package audit is the only writer of admin_audit (GF-2). Rows are
// append-only; the database rejects UPDATE, DELETE, and TRUNCATE (migration 003).
package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Record is one admin action. Target is "user:<id>" for user writes.
// At is optional: when zero, the column default (now()) is used. P3 writes set
// it from the injected clock (GF-45).
type Record struct {
	FleetID int64
	ActorID int64
	Action  string
	Target  string
	At      time.Time
}

// Write inserts one audit row inside tx, so the row commits with the change it describes.
func Write(ctx context.Context, tx *sql.Tx, r Record) error {
	if tx == nil {
		return errors.New("audit: nil transaction")
	}
	if r.Action == "" || r.Target == "" {
		return errors.New("audit: action and target are required")
	}
	var err error
	if r.At.IsZero() {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO admin_audit (fleet_id, actor_id, action, target) VALUES ($1, $2, $3, $4)`,
			r.FleetID, r.ActorID, r.Action, r.Target)
	} else {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO admin_audit (fleet_id, actor_id, action, target, at) VALUES ($1, $2, $3, $4, $5)`,
			r.FleetID, r.ActorID, r.Action, r.Target, r.At)
	}
	if err != nil {
		return fmt.Errorf("write audit row: %w", err)
	}
	return nil
}
