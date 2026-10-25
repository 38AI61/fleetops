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

// Outcome says whether a pairing call wrote anything (GF-38, GF-39).
type Outcome int

const (
	// NoOp means nothing changed and no audit row was written.
	NoOp Outcome = iota
	// Created means a row was inserted, ended, or both (pair, swap, or unpair).
	Created
)

// Pairing is one row of vehicle_pairings (D-28). Ended rows only ever gain ended_at.
type Pairing struct {
	ID        int64      `json:"id"`
	VehicleID int64      `json:"vehicle_id"`
	DriverID  *int64     `json:"driver_id"`
	DeviceID  int64      `json:"device_id"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
}

// PairingInput is the POST /vehicles/:id/pairings body. A nil DeviceID unpairs.
type PairingInput struct {
	DeviceID *int64
	DriverID *int64
}

const pairingCols = `id, vehicle_id, driver_id, device_id, started_at, ended_at`

func scanPairing(r rowScanner) (Pairing, error) {
	var p Pairing
	var driver sql.NullInt64
	var ended sql.NullTime
	if err := r.Scan(&p.ID, &p.VehicleID, &driver, &p.DeviceID, &p.StartedAt, &ended); err != nil {
		return Pairing{}, err
	}
	if driver.Valid {
		p.DriverID = &driver.Int64
	}
	if ended.Valid {
		p.EndedAt = &ended.Time
	}
	return p, nil
}

// SetPairing pairs, swaps, or unpairs a vehicle in one transaction (GF-38, GF-39).
// Lock order is vehicle then device, which prevents deadlock between calls.
// Created is returned when a row was inserted or ended; NoOp writes nothing.
// The returned *Pairing is the row inserted (pair) or ended (unpair), or the
// current row on NoOp; it is nil when there is nothing to report.
func (s *Service) SetPairing(ctx context.Context, actor Actor, vehicleID int64, in PairingInput) (*Pairing, Outcome, error) {
	if in.DeviceID == nil && in.DriverID != nil {
		return nil, NoOp, ErrValidation
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, NoOp, fmt.Errorf("begin pairing: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 1. Vehicle first, locked (GF-39).
	var status string
	err = tx.QueryRowContext(ctx,
		`SELECT status FROM vehicles WHERE id = $1 AND fleet_id = $2 FOR UPDATE`,
		vehicleID, actor.FleetID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, NoOp, ErrNotFound
	}
	if err != nil {
		return nil, NoOp, fmt.Errorf("lock vehicle: %w", err)
	}

	// 2. Device second, locked; revoked devices are refused.
	if in.DeviceID != nil {
		var revoked sql.NullTime
		err := tx.QueryRowContext(ctx,
			`SELECT revoked_at FROM devices WHERE id = $1 AND fleet_id = $2 FOR UPDATE`,
			*in.DeviceID, actor.FleetID).Scan(&revoked)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, NoOp, ErrValidation
		}
		if err != nil {
			return nil, NoOp, fmt.Errorf("lock device: %w", err)
		}
		if revoked.Valid {
			return nil, NoOp, ErrDeviceRevoked
		}
	}

	// 3. Driver must exist in the fleet.
	if in.DriverID != nil {
		var one int
		err := tx.QueryRowContext(ctx,
			`SELECT 1 FROM drivers WHERE id = $1 AND fleet_id = $2`, *in.DriverID, actor.FleetID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, NoOp, ErrValidation
		}
		if err != nil {
			return nil, NoOp, fmt.Errorf("check driver: %w", err)
		}
	}

	// 4. Only active vehicles take a new device.
	if in.DeviceID != nil && status != VehicleActive {
		return nil, NoOp, ErrVehicleNotActive
	}

	// 5. Current rows for this vehicle and this device.
	openOnVehicle, err := openPairingOnVehicle(ctx, tx, vehicleID)
	if err != nil {
		return nil, NoOp, err
	}
	if in.DeviceID != nil {
		openOnDevice, err := openPairingOnDevice(ctx, tx, *in.DeviceID)
		if err != nil {
			return nil, NoOp, err
		}
		if openOnDevice != nil && openOnDevice.VehicleID != vehicleID {
			return nil, NoOp, ErrDeviceInUse
		}
	}

	// 6. No-op cases.
	if in.DeviceID == nil && openOnVehicle == nil {
		return nil, NoOp, nil
	}
	if in.DeviceID != nil && openOnVehicle != nil &&
		openOnVehicle.DeviceID == *in.DeviceID && sameDriver(openOnVehicle.DriverID, in.DriverID) {
		return openOnVehicle, NoOp, nil
	}

	now := s.Clock()
	action := "vehicle.pair"
	if openOnVehicle != nil {
		if _, err := tx.ExecContext(ctx,
			`UPDATE vehicle_pairings SET ended_at = $2 WHERE id = $1`, openOnVehicle.ID, now); err != nil {
			return nil, NoOp, fmt.Errorf("end pairing: %w", err)
		}
		openOnVehicle.EndedAt = &now
	}

	var result *Pairing
	if in.DeviceID == nil {
		action = "vehicle.unpair"
		result = openOnVehicle
	} else {
		row, err := scanPairing(tx.QueryRowContext(ctx,
			`INSERT INTO vehicle_pairings (fleet_id, vehicle_id, driver_id, device_id, started_at)
			 VALUES ($1, $2, $3, $4, $5) RETURNING `+pairingCols,
			actor.FleetID, vehicleID, in.DriverID, *in.DeviceID, now))
		if err != nil {
			return nil, NoOp, mapPairingErr(err)
		}
		result = &row
	}

	var mirror *int64
	if in.DeviceID != nil {
		mirror = in.DeviceID
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE vehicles SET paired_device_id = $2 WHERE id = $1`, vehicleID, mirror); err != nil {
		return nil, NoOp, fmt.Errorf("set paired device: %w", err)
	}

	if err := audit.Write(ctx, tx, audit.Record{
		FleetID: actor.FleetID, ActorID: actor.UserID, Action: action, Target: vehicleTarget(vehicleID), At: now,
	}); err != nil {
		return nil, NoOp, err
	}
	if err := tx.Commit(); err != nil {
		return nil, NoOp, fmt.Errorf("commit pairing: %w", err)
	}
	return result, Created, nil
}

// ListPairings returns a vehicle's history, newest first (GF-38, GF-48). The
// vehicle must belong to the fleet.
func (s *Service) ListPairings(ctx context.Context, fleetID, vehicleID int64) ([]Pairing, error) {
	if _, err := s.GetVehicle(ctx, fleetID, vehicleID); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+pairingCols+` FROM vehicle_pairings
		  WHERE fleet_id = $1 AND vehicle_id = $2 ORDER BY id DESC`, fleetID, vehicleID)
	if err != nil {
		return nil, fmt.Errorf("list pairings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Pairing{}
	for rows.Next() {
		p, err := scanPairing(rows)
		if err != nil {
			return nil, fmt.Errorf("scan pairing: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func openPairingOnVehicle(ctx context.Context, tx *sql.Tx, vehicleID int64) (*Pairing, error) {
	p, err := scanPairing(tx.QueryRowContext(ctx,
		`SELECT `+pairingCols+` FROM vehicle_pairings WHERE vehicle_id = $1 AND ended_at IS NULL FOR UPDATE`, vehicleID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open pairing on vehicle: %w", err)
	}
	return &p, nil
}

func openPairingOnDevice(ctx context.Context, tx *sql.Tx, deviceID int64) (*Pairing, error) {
	p, err := scanPairing(tx.QueryRowContext(ctx,
		`SELECT `+pairingCols+` FROM vehicle_pairings WHERE device_id = $1 AND ended_at IS NULL FOR UPDATE`, deviceID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open pairing on device: %w", err)
	}
	return &p, nil
}

// mapPairingErr maps the partial unique indexes (D-28) to registry errors.
// Both indexes are backstops for the locked checks above (GF-39).
func mapPairingErr(err error) error {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		if pqErr.Constraint == "vehicle_pairings_open_device_key" {
			return ErrDeviceInUse
		}
		return ErrConflict
	}
	return fmt.Errorf("insert pairing: %w", err)
}

func sameDriver(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
