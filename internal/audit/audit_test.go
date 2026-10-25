package audit

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/lib/pq"

	"fleetops/internal/testinfra"
)

const fakeHash = "$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAA"

// seedActor inserts a fleet and an admin user and returns their IDs.
func seedActor(t *testing.T, db *sql.DB) (fleetID, actorID int64) {
	t.Helper()
	if err := db.QueryRow(`INSERT INTO fleets (name) VALUES ('A') RETURNING id`).Scan(&fleetID); err != nil {
		t.Fatalf("insert fleet: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO users (fleet_id, email, password_hash, role)
		VALUES ($1, 'admin@example.com', $2, 'admin') RETURNING id`, fleetID, fakeHash).Scan(&actorID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return fleetID, actorID
}

func writeRow(t *testing.T, db *sql.DB, fleetID, actorID int64) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := Write(context.Background(), tx, Record{
		FleetID: fleetID, ActorID: actorID, Action: "user.create", Target: "user:99",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// assertAppendOnly checks that err is the trigger's check violation.
func assertAppendOnly(t *testing.T, op string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s on admin_audit: want rejection, got nil", op)
	}
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		t.Fatalf("%s: want *pq.Error, got %T %v", op, err, err)
	}
	if pqErr.Code != "23514" {
		t.Errorf("%s: SQLSTATE = %s, want 23514", op, pqErr.Code)
	}
	if !strings.Contains(pqErr.Message, "append-only") {
		t.Errorf("%s: message %q does not contain \"append-only\"", op, pqErr.Message)
	}
}

func TestInsertSucceeds(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleetID, actorID := seedActor(t, db)
	writeRow(t, db, fleetID, actorID)

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM admin_audit`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func TestUpdateRejected(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleetID, actorID := seedActor(t, db)
	writeRow(t, db, fleetID, actorID)

	_, err := db.Exec(`UPDATE admin_audit SET action = 'x'`)
	assertAppendOnly(t, "UPDATE", err)
}

func TestDeleteRejected(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleetID, actorID := seedActor(t, db)
	writeRow(t, db, fleetID, actorID)

	_, err := db.Exec(`DELETE FROM admin_audit`)
	assertAppendOnly(t, "DELETE", err)
}

func TestTruncateRejected(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleetID, actorID := seedActor(t, db)
	writeRow(t, db, fleetID, actorID)

	_, err := db.Exec(`TRUNCATE admin_audit`)
	assertAppendOnly(t, "TRUNCATE", err)
}

func TestWriteRejectsEmptyRecord(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := Write(context.Background(), tx, Record{FleetID: 1, ActorID: 1}); err == nil {
		t.Fatal("empty action: want error")
	}
	if err := Write(context.Background(), nil, Record{FleetID: 1, ActorID: 1, Action: "a", Target: "t"}); err == nil {
		t.Fatal("nil tx: want error")
	}
}
