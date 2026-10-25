package users

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/lib/pq"

	"fleetops/internal/testinfra"
)

const fakeHash = "$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAA"

func newFleet(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`INSERT INTO fleets (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert fleet: %v", err)
	}
	return id
}

func createUser(t *testing.T, s *Store, fleetID int64, email, role string) User {
	t.Helper()
	tx, err := s.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	u, err := s.Create(context.Background(), tx, fleetID, email, fakeHash, role)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("create %s: %v", email, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return u
}

func TestCreateThenListIsScopedToFleet(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	s := NewStore(db)
	ctx := context.Background()

	fleetA := newFleet(t, db, "A")
	fleetB := newFleet(t, db, "B")
	a := createUser(t, s, fleetA, "admin@a.example", "admin")
	createUser(t, s, fleetB, "admin@b.example", "admin")

	list, err := s.List(ctx, fleetA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("fleet A list = %+v, want only %d", list, a.ID)
	}
	if list[0].Email != "admin@a.example" || list[0].Role != "admin" {
		t.Fatalf("unexpected row %+v", list[0])
	}
}

func TestDuplicateEmailIsCaseInsensitiveConflict(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	s := NewStore(db)
	fleet := newFleet(t, db, "A")
	createUser(t, s, fleet, "Dispatch@Example.com", "dispatcher")

	tx, err := s.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = s.Create(context.Background(), tx, fleet, "  dispatch@example.COM ", fakeHash, "manager")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate email: want ErrConflict, got %v", err)
	}
}

func TestCheckConstraintRejectsUnknownRole(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	s := NewStore(db)
	fleet := newFleet(t, db, "A")

	tx, err := s.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = s.Create(context.Background(), tx, fleet, "x@example.com", fakeHash, "superuser")
	if err == nil {
		t.Fatal("unknown role: want check violation")
	}
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) || pqErr.Code != "23514" {
		t.Fatalf("unknown role: want SQLSTATE 23514, got %v", err)
	}
}

func TestDriverIDStaysNull(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	s := NewStore(db)
	fleet := newFleet(t, db, "A")
	u := createUser(t, s, fleet, "m@example.com", "manager")

	var isNull bool
	if err := db.QueryRow(`SELECT driver_id IS NULL FROM users WHERE id = $1`, u.ID).Scan(&isNull); err != nil {
		t.Fatalf("query driver_id: %v", err)
	}
	if !isNull {
		t.Fatal("driver_id must stay NULL in P2")
	}
}

func TestGetAndUpdateRefuseOtherFleet(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	s := NewStore(db)
	ctx := context.Background()
	fleetA := newFleet(t, db, "A")
	fleetB := newFleet(t, db, "B")
	b := createUser(t, s, fleetB, "b@example.com", "manager")

	if _, err := s.Get(ctx, fleetA, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get cross-fleet: want ErrNotFound, got %v", err)
	}

	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	role := "driver"
	if _, err := s.Update(ctx, tx, fleetA, b.ID, Patch{Role: &role}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update cross-fleet: want ErrNotFound, got %v", err)
	}
}

func TestGetByEmailIsCaseInsensitive(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	s := NewStore(db)
	fleet := newFleet(t, db, "A")
	created := createUser(t, s, fleet, "Ops@Example.com", "dispatcher")

	got, err := s.GetByEmail(context.Background(), "OPS@example.com")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if got.ID != created.ID || got.PasswordHash != fakeHash {
		t.Fatalf("GetByEmail = %+v", got)
	}
}
