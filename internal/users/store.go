// Package users stores accounts and serves the admin user endpoints (D-01, D-06).
// Every query is scoped by fleet_id (README §8, GF-27).
package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Sentinel errors returned by the store.
var (
	ErrNotFound = errors.New("user not found")
	ErrConflict = errors.New("email already in use")
)

const uniqueViolation = "23505"

// User is one account. PasswordHash is never serialized.
type User struct {
	ID           int64      `json:"id"`
	FleetID      int64      `json:"fleet_id"`
	Email        string     `json:"email"`
	Role         string     `json:"role"`
	DisabledAt   *time.Time `json:"disabled_at"`
	CreatedAt    time.Time  `json:"created_at"`
	PasswordHash string     `json:"-"`
}

// Disabled reports whether the account has been disabled.
func (u User) Disabled() bool { return u.DisabledAt != nil }

// Patch changes any of role, disabled, or password hash. Nil fields are unchanged.
type Patch struct {
	Role         *string
	Disabled     *bool
	PasswordHash *string
}

// Store is the users table accessor. Reads run on the pool; writes take the
// caller's transaction so the audit row can commit with them.
type Store struct {
	db *sql.DB
}

// NewStore wraps a database handle.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Begin opens a transaction for a write plus its audit row.
func (s *Store) Begin(ctx context.Context) (*sql.Tx, error) {
	return s.db.BeginTx(ctx, nil)
}

// NormalizeEmail trims and lowercases an email (GF-27).
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// Create inserts a user in the given fleet. Email uniqueness is case-insensitive
// through CITEXT; a duplicate returns ErrConflict. driver_id stays NULL (P2).
func (s *Store) Create(ctx context.Context, tx *sql.Tx, fleetID int64, email, passwordHash, role string) (User, error) {
	var u User
	err := tx.QueryRowContext(ctx,
		`INSERT INTO users (fleet_id, email, password_hash, role)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, fleet_id, email, role, disabled_at, created_at, password_hash`,
		fleetID, NormalizeEmail(email), passwordHash, role,
	).Scan(&u.ID, &u.FleetID, &u.Email, &u.Role, &u.DisabledAt, &u.CreatedAt, &u.PasswordHash)
	if err != nil {
		if isCode(err, uniqueViolation) {
			return User{}, ErrConflict
		}
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	return u, nil
}

// List returns every user in the fleet, oldest first.
func (s *Store) List(ctx context.Context, fleetID int64) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, fleet_id, email, role, disabled_at, created_at, password_hash
		   FROM users WHERE fleet_id = $1 ORDER BY id`, fleetID)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.FleetID, &u.Email, &u.Role, &u.DisabledAt, &u.CreatedAt, &u.PasswordHash); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return out, nil
}

// Get returns one user only if it belongs to the fleet; otherwise ErrNotFound.
func (s *Store) Get(ctx context.Context, fleetID, id int64) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, fleet_id, email, role, disabled_at, created_at, password_hash
		   FROM users WHERE id = $1 AND fleet_id = $2`, id, fleetID,
	).Scan(&u.ID, &u.FleetID, &u.Email, &u.Role, &u.DisabledAt, &u.CreatedAt, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

// GetByEmail is the login lookup. It is not fleet-scoped because email is
// globally unique and the login has no fleet yet (D-01).
func (s *Store) GetByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, fleet_id, email, role, disabled_at, created_at, password_hash
		   FROM users WHERE email = $1`, NormalizeEmail(email),
	).Scan(&u.ID, &u.FleetID, &u.Email, &u.Role, &u.DisabledAt, &u.CreatedAt, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get user by email: %w", err)
	}
	return u, nil
}

// Update applies a patch inside tx to a user in the fleet. Disabling sets
// disabled_at once and re-enabling clears it. Unknown or other-fleet IDs return ErrNotFound.
func (s *Store) Update(ctx context.Context, tx *sql.Tx, fleetID, id int64, p Patch) (User, error) {
	var u User
	err := tx.QueryRowContext(ctx,
		`UPDATE users SET
		     role          = COALESCE($3::text, role),
		     password_hash = COALESCE($4::text, password_hash),
		     disabled_at   = CASE
		         WHEN $5::boolean IS NULL THEN disabled_at
		         WHEN $5::boolean         THEN COALESCE(disabled_at, now())
		         ELSE NULL END
		 WHERE id = $1 AND fleet_id = $2
		 RETURNING id, fleet_id, email, role, disabled_at, created_at, password_hash`,
		id, fleetID, p.Role, p.PasswordHash, p.Disabled,
	).Scan(&u.ID, &u.FleetID, &u.Email, &u.Role, &u.DisabledAt, &u.CreatedAt, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("update user: %w", err)
	}
	return u, nil
}

// SetDisabled disables or re-enables a user inside tx.
func (s *Store) SetDisabled(ctx context.Context, tx *sql.Tx, fleetID, id int64, disabled bool) (User, error) {
	return s.Update(ctx, tx, fleetID, id, Patch{Disabled: &disabled})
}

// SetPassword replaces the password hash inside tx.
func (s *Store) SetPassword(ctx context.Context, tx *sql.Tx, fleetID, id int64, passwordHash string) (User, error) {
	return s.Update(ctx, tx, fleetID, id, Patch{PasswordHash: &passwordHash})
}

func isCode(err error, code string) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && string(pqErr.Code) == code
}

// Roles accepted by the users table CHECK constraint (migration 002).
var roles = []string{"admin", "manager", "dispatcher", "driver"}

// ValidRole reports whether role is one of the four P2 roles.
func ValidRole(role string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}
