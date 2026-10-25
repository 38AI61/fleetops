package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"fleetops/internal/testinfra"
)

const testHash = "$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAA"

var t0 = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

// seedUser inserts a fleet and one user and returns their IDs.
func seedUser(t *testing.T, db *sql.DB, email string) (fleetID, userID int64) {
	t.Helper()
	if err := db.QueryRow(`INSERT INTO fleets (name) VALUES ('F') RETURNING id`).Scan(&fleetID); err != nil {
		t.Fatalf("insert fleet: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO users (fleet_id, email, password_hash, role)
		VALUES ($1, $2, $3, 'dispatcher') RETURNING id`, fleetID, email, testHash).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return fleetID, userID
}

// issue creates a refresh token in its own transaction, as login does.
func issue(t *testing.T, db *sql.DB, userID, fleetID int64, now time.Time) string {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	raw, err := Issue(context.Background(), tx, userID, fleetID, now)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("Issue: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit issue: %v", err)
	}
	return raw
}

func TestRotationIssuesNewTokenAndMarksOld(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleetID, userID := seedUser(t, db, "r@example.com")
	old := issue(t, db, userID, fleetID, t0)

	_, _, fresh, err := Rotate(context.Background(), db, old, t0.Add(time.Minute))
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if fresh == "" || fresh == old {
		t.Fatalf("new token = %q, want a distinct value", fresh)
	}

	var rotated int
	if err := db.QueryRow(`SELECT count(*) FROM refresh_tokens
		WHERE token_hash = $1 AND rotated_at IS NOT NULL`, hashToken(old)).Scan(&rotated); err != nil {
		t.Fatalf("query old: %v", err)
	}
	if rotated != 1 {
		t.Fatal("old token not marked rotated")
	}
}

func TestOldTokenAfterRotationIsRefused(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleetID, userID := seedUser(t, db, "r@example.com")
	old := issue(t, db, userID, fleetID, t0)
	if _, _, _, err := Rotate(context.Background(), db, old, t0.Add(time.Minute)); err != nil {
		t.Fatalf("first Rotate: %v", err)
	}

	_, _, _, err := Rotate(context.Background(), db, old, t0.Add(2*time.Minute))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("replay of old token: want ErrInvalid, got %v", err)
	}
	if !errors.Is(err, ErrReuseDetected) {
		t.Fatalf("replay of a rotated token is reuse: want ErrReuseDetected, got %v", err)
	}
}

func TestReuseRevokesAllTokensIncludingOtherSession(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleetID, userID := seedUser(t, db, "r@example.com")

	laptop := issue(t, db, userID, fleetID, t0)
	phone := issue(t, db, userID, fleetID, t0) // separate family

	_, _, laptopNext, err := Rotate(context.Background(), db, laptop, t0.Add(time.Minute))
	if err != nil {
		t.Fatalf("rotate laptop: %v", err)
	}

	// Replaying the laptop's old token is theft: everything goes.
	_, _, _, err = Rotate(context.Background(), db, laptop, t0.Add(2*time.Minute))
	if !errors.Is(err, ErrReuseDetected) {
		t.Fatalf("reuse: want ErrReuseDetected, got %v", err)
	}

	for name, raw := range map[string]string{"phone": phone, "laptop-rotated": laptopNext} {
		if _, _, _, err := Rotate(context.Background(), db, raw, t0.Add(3*time.Minute)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s token after reuse: want ErrInvalid, got %v", name, err)
		}
	}
	var live int
	if err := db.QueryRow(`SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND revoked_at IS NULL`,
		userID).Scan(&live); err != nil {
		t.Fatalf("count live: %v", err)
	}
	if live != 0 {
		t.Fatalf("live tokens after reuse = %d, want 0", live)
	}
}

func TestExpiredTokenRefused(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleetID, userID := seedUser(t, db, "r@example.com")
	raw := issue(t, db, userID, fleetID, t0)

	// Compare against the injected clock, so the past time is relative to t0.
	if _, err := db.Exec(`UPDATE refresh_tokens SET expires_at = $2
		WHERE token_hash = $1`, hashToken(raw), t0.Add(-time.Second)); err != nil {
		t.Fatalf("expire token: %v", err)
	}
	if _, _, _, err := Rotate(context.Background(), db, raw, t0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expired token: want ErrInvalid, got %v", err)
	}
}

func TestDisabledUserRefusedAndFamilyRevoked(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleetID, userID := seedUser(t, db, "r@example.com")
	raw := issue(t, db, userID, fleetID, t0)

	if _, err := db.Exec(`UPDATE users SET disabled_at = now() WHERE id = $1`, userID); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, _, _, err := Rotate(context.Background(), db, raw, t0.Add(time.Minute)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("disabled user: want ErrInvalid, got %v", err)
	}
	var live int
	if err := db.QueryRow(`SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND revoked_at IS NULL`,
		userID).Scan(&live); err != nil {
		t.Fatalf("count live: %v", err)
	}
	if live != 0 {
		t.Fatalf("live tokens after disabled refresh = %d, want 0 (revocation must commit)", live)
	}
}

func TestLogoutRevokesFamilyThenRefreshFails(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleetID, userID := seedUser(t, db, "r@example.com")
	raw := issue(t, db, userID, fleetID, t0)

	if err := RevokeFamily(context.Background(), db, raw, t0.Add(time.Minute)); err != nil {
		t.Fatalf("RevokeFamily: %v", err)
	}
	if _, _, _, err := Rotate(context.Background(), db, raw, t0.Add(2*time.Minute)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("refresh after logout: want ErrInvalid, got %v", err)
	}
}

func TestLogoutOfUnknownTokenIsNoOp(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	if err := RevokeFamily(context.Background(), db, "does-not-exist", t0); err != nil {
		t.Fatalf("unknown token logout: want nil, got %v", err)
	}
}

func TestRawTokenNeverStored(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleetID, userID := seedUser(t, db, "r@example.com")
	raw := issue(t, db, userID, fleetID, t0)

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM refresh_tokens WHERE token_hash = $1`,
		[]byte(raw)).Scan(&n); err != nil {
		t.Fatalf("query raw: %v", err)
	}
	if n != 0 {
		t.Fatal("raw token found in refresh_tokens.token_hash")
	}

	sum := sha256.Sum256([]byte(raw))
	var stored []byte
	if err := db.QueryRow(`SELECT token_hash FROM refresh_tokens WHERE user_id = $1`, userID).Scan(&stored); err != nil {
		t.Fatalf("query stored hash: %v", err)
	}
	if !bytes.Equal(stored, sum[:]) {
		t.Fatal("stored value is not SHA-256 of the raw token")
	}
	if strings.Contains(string(stored), raw) {
		t.Fatal("raw token appears inside stored value")
	}
}

func TestRawTokenShape(t *testing.T) {
	raw, err := newRawToken()
	if err != nil {
		t.Fatalf("newRawToken: %v", err)
	}
	if len(raw) != 43 { // 32 bytes, base64url, no padding
		t.Fatalf("raw token length = %d, want 43", len(raw))
	}
}
