package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"fleetops/internal/devicekey"
)

// Device kinds (GF-33).
const (
	KindPWA     = "pwa"
	KindTracker = "tracker"
)

// zeroHash is compared when a key names an unknown device, so a miss costs one
// HMAC compare just like a hit (GF-41, timing equalization).
var zeroHash = make([]byte, 32)

// Device is one phone or tracker. The key hash is never returned.
type Device struct {
	ID        int64      `json:"id"`
	FleetID   int64      `json:"fleet_id"`
	Kind      string     `json:"kind"`
	RevokedAt *time.Time `json:"revoked_at"`
}

// IssuedDevice carries the plaintext key, shown exactly once (GF-32).
type IssuedDevice struct {
	Device
	APIKey string `json:"api_key"`
}

// IssueDevice creates a device and returns its key once (D-29, GF-32). The key
// id is known only after the insert, so the stored hash covers the secret alone.
func (s *Service) IssueDevice(ctx context.Context, actor Actor, kind string) (IssuedDevice, error) {
	if kind != KindPWA && kind != KindTracker {
		return IssuedDevice{}, ErrValidation
	}
	secret, encoded, err := devicekey.GenerateSecret()
	if err != nil {
		return IssuedDevice{}, fmt.Errorf("generate device secret: %w", err)
	}
	hash := devicekey.Hash(s.Salt, encoded)

	var out IssuedDevice
	err = s.write(ctx, actor, "device.issue", func(tx *sql.Tx) (string, error) {
		var d Device
		err := tx.QueryRowContext(ctx,
			`INSERT INTO devices (fleet_id, kind, api_key_hash) VALUES ($1, $2, $3)
			 RETURNING id, fleet_id, kind, revoked_at`,
			actor.FleetID, kind, hash,
		).Scan(&d.ID, &d.FleetID, &d.Kind, &d.RevokedAt)
		if err != nil {
			return "", mapWriteErr(err)
		}
		out = IssuedDevice{Device: d, APIKey: devicekey.Format(d.ID, secret)}
		return deviceTarget(d.ID), nil
	})
	return out, err
}

// RotateDevice replaces the key hash immediately; the old key stops working (D-29).
// A revoked device cannot rotate (GF-32, device_revoked).
func (s *Service) RotateDevice(ctx context.Context, actor Actor, id int64) (IssuedDevice, error) {
	var out IssuedDevice
	err := s.write(ctx, actor, "device.rotate", func(tx *sql.Tx) (string, error) {
		d, err := lockDevice(ctx, tx, actor.FleetID, id)
		if err != nil {
			return "", err
		}
		if d.RevokedAt != nil {
			return "", ErrDeviceRevoked
		}
		secret, encoded, err := devicekey.GenerateSecret()
		if err != nil {
			return "", fmt.Errorf("generate device secret: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE devices SET api_key_hash = $3 WHERE id = $1 AND fleet_id = $2`,
			id, actor.FleetID, devicekey.Hash(s.Salt, encoded)); err != nil {
			return "", fmt.Errorf("rotate device: %w", err)
		}
		out = IssuedDevice{Device: d, APIKey: devicekey.Format(d.ID, secret)}
		return deviceTarget(d.ID), nil
	})
	return out, err
}

// RevokeDevice sets revoked_at on the first call; later calls keep the original
// timestamp (GF-32). Existing pairing rows are left untouched.
func (s *Service) RevokeDevice(ctx context.Context, actor Actor, id int64) (Device, error) {
	var out Device
	err := s.write(ctx, actor, "device.revoke", func(tx *sql.Tx) (string, error) {
		now := s.Clock()
		err := tx.QueryRowContext(ctx,
			`UPDATE devices SET revoked_at = COALESCE(revoked_at, $3)
			  WHERE id = $1 AND fleet_id = $2
			  RETURNING id, fleet_id, kind, revoked_at`,
			id, actor.FleetID, now,
		).Scan(&out.ID, &out.FleetID, &out.Kind, &out.RevokedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		if err != nil {
			return "", fmt.Errorf("revoke device: %w", err)
		}
		return deviceTarget(out.ID), nil
	})
	return out, err
}

// CheckDeviceActive is the P4 ingest gate (GF-41). It reads only; it does not
// touch last_seen_at.
func CheckDeviceActive(ctx context.Context, q querier, deviceID int64) error {
	var revoked sql.NullTime
	err := q.QueryRowContext(ctx, `SELECT revoked_at FROM devices WHERE id = $1`, deviceID).Scan(&revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDeviceNotFound
	}
	if err != nil {
		return fmt.Errorf("check device: %w", err)
	}
	if revoked.Valid {
		return ErrDeviceRevoked
	}
	return nil
}

// AuthenticateDeviceKey parses a key, checks its secret against the stored
// hash in constant time, then refuses revoked devices (GF-41). The revoked
// check comes after the key check so a wrong key never reveals revocation.
func AuthenticateDeviceKey(ctx context.Context, db *sql.DB, salt []byte, key string) (Device, error) {
	id, secret, err := devicekey.Parse(key)
	if err != nil {
		return Device{}, ErrDeviceKeyMalformed
	}

	var d Device
	var stored []byte
	var revoked sql.NullTime
	err = db.QueryRowContext(ctx,
		`SELECT id, fleet_id, kind, revoked_at, api_key_hash FROM devices WHERE id = $1`, id,
	).Scan(&d.ID, &d.FleetID, &d.Kind, &revoked, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		devicekey.Verify(salt, secret, zeroHash)
		return Device{}, ErrDeviceKeyInvalid
	}
	if err != nil {
		return Device{}, fmt.Errorf("authenticate device: %w", err)
	}
	if !devicekey.Verify(salt, secret, stored) {
		return Device{}, ErrDeviceKeyInvalid
	}
	if revoked.Valid {
		return Device{}, ErrDeviceRevoked
	}
	return d, nil
}

// lockDevice reads one device in the fleet under a row lock.
func lockDevice(ctx context.Context, tx *sql.Tx, fleetID, id int64) (Device, error) {
	var d Device
	err := tx.QueryRowContext(ctx,
		`SELECT id, fleet_id, kind, revoked_at FROM devices WHERE id = $1 AND fleet_id = $2 FOR UPDATE`,
		id, fleetID,
	).Scan(&d.ID, &d.FleetID, &d.Kind, &d.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	if err != nil {
		return Device{}, fmt.Errorf("lock device: %w", err)
	}
	return d, nil
}

func deviceTarget(id int64) string {
	return fmt.Sprintf("device:%d", id)
}
