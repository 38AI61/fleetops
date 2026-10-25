package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// RefreshTTL is how long each refresh token lives after its own issuance (GF-26).
const RefreshTTL = 7 * 24 * time.Hour

// ErrInvalid means a refresh token cannot be used: unknown, expired, revoked,
// or belonging to a disabled user.
var ErrInvalid = errors.New("invalid refresh token")

// ErrReuseDetected means a rotated token was presented again. All of the
// user's tokens have been revoked (D-02). It wraps ErrInvalid so callers can
// test either.
var ErrReuseDetected = fmt.Errorf("%w: refresh token reuse detected", ErrInvalid)

// Issue creates a refresh token in a new family for the user and returns the
// raw token. Only its SHA-256 is stored (D-02).
func Issue(ctx context.Context, tx *sql.Tx, userID, fleetID int64, now time.Time) (string, error) {
	family, err := newFamilyID()
	if err != nil {
		return "", err
	}
	return issueInFamily(ctx, tx, userID, fleetID, family, now)
}

func issueInFamily(ctx context.Context, tx *sql.Tx, userID, fleetID int64, family string, now time.Time) (string, error) {
	raw, err := newRawToken()
	if err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO refresh_tokens (fleet_id, user_id, family_id, token_hash, issued_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		fleetID, userID, family, hashToken(raw), now, now.Add(RefreshTTL))
	if err != nil {
		return "", fmt.Errorf("insert refresh token: %w", err)
	}
	return raw, nil
}

// Rotate exchanges a valid refresh token for a new one in the same family.
// GF-26: one conditional UPDATE does the rotation. If the hash exists but was
// already rotated, that is reuse: every token of the user is revoked. A disabled
// user's family is revoked. Revocations commit even when the call refuses.
func Rotate(ctx context.Context, db *sql.DB, raw string, now time.Time) (userID, fleetID int64, newRaw string, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, "", fmt.Errorf("begin rotate: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var family string
	var disabled bool
	err = tx.QueryRowContext(ctx,
		`UPDATE refresh_tokens SET rotated_at = $2
		  WHERE token_hash = $1 AND rotated_at IS NULL AND revoked_at IS NULL AND expires_at > $2
		  RETURNING user_id, fleet_id, family_id::text,
		            (SELECT disabled_at IS NOT NULL FROM users WHERE users.id = refresh_tokens.user_id)`,
		hashToken(raw), now,
	).Scan(&userID, &fleetID, &family, &disabled)

	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, "", refuse(ctx, tx, raw, now)
	}
	if err != nil {
		return 0, 0, "", fmt.Errorf("rotate refresh token: %w", err)
	}

	if disabled {
		if err := revokeFamilyTx(ctx, tx, family, now); err != nil {
			return 0, 0, "", err
		}
		if err := tx.Commit(); err != nil {
			return 0, 0, "", fmt.Errorf("commit disabled-user revoke: %w", err)
		}
		return 0, 0, "", ErrInvalid
	}

	newRaw, err = issueInFamily(ctx, tx, userID, fleetID, family, now)
	if err != nil {
		return 0, 0, "", err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, "", fmt.Errorf("commit rotate: %w", err)
	}
	return userID, fleetID, newRaw, nil
}

// refuse handles a rotation that matched no row. It commits what it revokes.
func refuse(ctx context.Context, tx *sql.Tx, raw string, now time.Time) error {
	var userID int64
	var rotated bool
	err := tx.QueryRowContext(ctx,
		`SELECT user_id, rotated_at IS NOT NULL FROM refresh_tokens WHERE token_hash = $1`,
		hashToken(raw),
	).Scan(&userID, &rotated)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalid
	}
	if err != nil {
		return fmt.Errorf("look up refresh token: %w", err)
	}
	if !rotated {
		return ErrInvalid
	}
	if err := RevokeAllForUser(ctx, tx, userID, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reuse revoke: %w", err)
	}
	return ErrReuseDetected
}

// RevokeFamily revokes the family of the presented token (logout). An unknown
// token is a no-op, so logout is idempotent.
func RevokeFamily(ctx context.Context, db *sql.DB, raw string, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin logout: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var family string
	err = tx.QueryRowContext(ctx,
		`SELECT family_id::text FROM refresh_tokens WHERE token_hash = $1`,
		hashToken(raw),
	).Scan(&family)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("look up family: %w", err)
	}
	if err := revokeFamilyTx(ctx, tx, family, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit logout: %w", err)
	}
	return nil
}

// RevokeAllForUser revokes every live refresh token of the user, across all
// families. Disable, password reset, and reuse detection use it (D-02, GF-27).
func RevokeAllForUser(ctx context.Context, tx *sql.Tx, userID int64, now time.Time) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE refresh_tokens SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`,
		userID, now)
	if err != nil {
		return fmt.Errorf("revoke user tokens: %w", err)
	}
	return nil
}

func revokeFamilyTx(ctx context.Context, tx *sql.Tx, family string, now time.Time) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE refresh_tokens SET revoked_at = $2
		  WHERE family_id = $1::uuid AND revoked_at IS NULL`,
		family, now)
	if err != nil {
		return fmt.Errorf("revoke family: %w", err)
	}
	return nil
}

// newRawToken returns 32 random bytes, base64url without padding (D-02).
func newRawToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("refresh token entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// newFamilyID returns a random RFC 4122 version-4 UUID string.
func newFamilyID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("family id entropy: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}
