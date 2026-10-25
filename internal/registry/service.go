package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"fleetops/internal/audit"
)

// Actor is the authenticated caller of a write. FleetID comes from the
// principal, never from a request body (GF-27).
type Actor struct {
	UserID  int64
	FleetID int64
}

// Service runs registry reads and writes. Every write opens one transaction
// that also writes its audit row (GF-44). Timestamps come from Clock (GF-45).
type Service struct {
	DB    *sql.DB
	Salt  []byte
	Clock func() time.Time
}

// querier is satisfied by *sql.DB and *sql.Tx, so reads and writes share one code path.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type rowScanner interface {
	Scan(dest ...any) error
}

// write runs fn in one transaction and appends the audit row only when fn
// succeeds, so failed writes leave no audit trace (GF-44). fn returns the audit
// target of the row it changed.
func (s *Service) write(ctx context.Context, actor Actor, action string, fn func(tx *sql.Tx) (string, error)) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	target, err := fn(tx)
	if err != nil {
		return err
	}
	if err := audit.Write(ctx, tx, audit.Record{
		FleetID: actor.FleetID, ActorID: actor.UserID, Action: action, Target: target, At: s.Clock(),
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit write: %w", err)
	}
	return nil
}

// mapWriteErr turns PostgreSQL constraint failures into registry sentinels.
func mapWriteErr(err error) error {
	if err == nil {
		return nil
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		switch pqErr.Code {
		case "23505":
			return ErrConflict
		case "23514":
			return ErrValidation
		}
	}
	return err
}

// LinkedDriverID returns the driver a user is linked to (GF-43), or nil when unlinked.
func (s *Service) LinkedDriverID(ctx context.Context, fleetID, userID int64) (*int64, error) {
	var id sql.NullInt64
	err := s.DB.QueryRowContext(ctx,
		`SELECT driver_id FROM users WHERE id = $1 AND fleet_id = $2`, userID, fleetID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !id.Valid) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("linked driver: %w", err)
	}
	return &id.Int64, nil
}
