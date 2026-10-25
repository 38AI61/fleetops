package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Registry status values (D-27, GF-36).
const (
	VehicleActive      = "active"
	VehicleMaintenance = "maintenance"
	VehicleRetired     = "retired"
)

// Vehicle is one van in the registry. Display status is derived in P8 (D-27).
type Vehicle struct {
	ID             int64   `json:"id"`
	FleetID        int64   `json:"fleet_id"`
	Plate          string  `json:"plate"`
	Model          *string `json:"model"`
	Status         string  `json:"status"`
	PairedDeviceID *int64  `json:"paired_device_id"`
}

// VehicleInput is a create request. Empty strings mean "absent".
type VehicleInput struct {
	Plate  string
	Model  string
	Status string
}

// VehiclePatch is a partial update. A nil field is unchanged; Model "" clears it.
type VehiclePatch struct {
	Plate  *string
	Model  *string
	Status *string
}

var platePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9 -]{0,14}[A-Z0-9]$`)

// NormalizePlate trims, ASCII-uppercases, and checks the GF-35 grammar.
func NormalizePlate(raw string) (string, error) {
	s := asciiUpper(strings.TrimSpace(raw))
	if !platePattern.MatchString(s) {
		return "", ErrValidation
	}
	return s, nil
}

// ValidVehicleStatus reports whether s is one of the three registry statuses (GF-36).
func ValidVehicleStatus(s string) bool {
	return s == VehicleActive || s == VehicleMaintenance || s == VehicleRetired
}

const vehicleCols = `id, fleet_id, plate, model, status, paired_device_id`

func scanVehicle(r rowScanner) (Vehicle, error) {
	var v Vehicle
	var model sql.NullString
	var paired sql.NullInt64
	if err := r.Scan(&v.ID, &v.FleetID, &v.Plate, &model, &v.Status, &paired); err != nil {
		return Vehicle{}, err
	}
	if model.Valid {
		v.Model = &model.String
	}
	if paired.Valid {
		v.PairedDeviceID = &paired.Int64
	}
	return v, nil
}

// CreateVehicle inserts a vehicle and audits vehicle.create (GF-36, GF-37, GF-44).
func (s *Service) CreateVehicle(ctx context.Context, actor Actor, in VehicleInput) (Vehicle, error) {
	plate, err := NormalizePlate(in.Plate)
	if err != nil {
		return Vehicle{}, err
	}
	status := in.Status
	if status == "" {
		status = VehicleActive
	} else if !ValidVehicleStatus(status) {
		return Vehicle{}, ErrValidation
	}
	var model *string
	if m := strings.TrimSpace(in.Model); m != "" {
		model = &m
	}

	var out Vehicle
	err = s.write(ctx, actor, "vehicle.create", func(tx *sql.Tx) (string, error) {
		v, err := scanVehicle(tx.QueryRowContext(ctx,
			`INSERT INTO vehicles (fleet_id, plate, model, status) VALUES ($1, $2, $3, $4) RETURNING `+vehicleCols,
			actor.FleetID, plate, model, status))
		if err != nil {
			return "", mapWriteErr(err)
		}
		out = v
		return vehicleTarget(v.ID), nil
	})
	return out, err
}

// UpdateVehicle applies a patch under a row lock and audits vehicle.update.
func (s *Service) UpdateVehicle(ctx context.Context, actor Actor, id int64, p VehiclePatch) (Vehicle, error) {
	if p.Plate != nil {
		plate, err := NormalizePlate(*p.Plate)
		if err != nil {
			return Vehicle{}, err
		}
		p.Plate = &plate
	}
	if p.Status != nil && !ValidVehicleStatus(*p.Status) {
		return Vehicle{}, ErrValidation
	}

	var out Vehicle
	err := s.write(ctx, actor, "vehicle.update", func(tx *sql.Tx) (string, error) {
		cur, err := scanVehicle(tx.QueryRowContext(ctx,
			`SELECT `+vehicleCols+` FROM vehicles WHERE id = $1 AND fleet_id = $2 FOR UPDATE`, id, actor.FleetID))
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		if err != nil {
			return "", fmt.Errorf("lock vehicle: %w", err)
		}
		if p.Plate != nil {
			cur.Plate = *p.Plate
		}
		if p.Model != nil {
			cur.Model = nil
			if m := strings.TrimSpace(*p.Model); m != "" {
				cur.Model = &m
			}
		}
		if p.Status != nil {
			cur.Status = *p.Status
		}
		v, err := scanVehicle(tx.QueryRowContext(ctx,
			`UPDATE vehicles SET plate = $3, model = $4, status = $5 WHERE id = $1 AND fleet_id = $2 RETURNING `+vehicleCols,
			id, actor.FleetID, cur.Plate, cur.Model, cur.Status))
		if err != nil {
			return "", mapWriteErr(err)
		}
		out = v
		return vehicleTarget(v.ID), nil
	})
	return out, err
}

// ListVehicles returns every vehicle in the fleet, ordered by id (GF-48).
func (s *Service) ListVehicles(ctx context.Context, fleetID int64) ([]Vehicle, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+vehicleCols+` FROM vehicles WHERE fleet_id = $1 ORDER BY id`, fleetID)
	if err != nil {
		return nil, fmt.Errorf("list vehicles: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Vehicle{}
	for rows.Next() {
		v, err := scanVehicle(rows)
		if err != nil {
			return nil, fmt.Errorf("scan vehicle: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetVehicle returns one vehicle only if it belongs to the fleet (ErrNotFound otherwise).
func (s *Service) GetVehicle(ctx context.Context, fleetID, id int64) (Vehicle, error) {
	v, err := scanVehicle(s.DB.QueryRowContext(ctx,
		`SELECT `+vehicleCols+` FROM vehicles WHERE id = $1 AND fleet_id = $2`, id, fleetID))
	if errors.Is(err, sql.ErrNoRows) {
		return Vehicle{}, ErrNotFound
	}
	if err != nil {
		return Vehicle{}, fmt.Errorf("get vehicle: %w", err)
	}
	return v, nil
}

func vehicleTarget(id int64) string {
	return fmt.Sprintf("vehicle:%d", id)
}
