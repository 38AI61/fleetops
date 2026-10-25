package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Driver is one driver row. The license number is never returned (GF-34).
type Driver struct {
	ID         int64   `json:"id"`
	FleetID    int64   `json:"fleet_id"`
	Name       string  `json:"name"`
	Phone      *string `json:"phone"`
	ShiftStart *string `json:"shift_start"`
	ShiftEnd   *string `json:"shift_end"`
}

// DriverInput is a create request. Empty strings mean "absent".
type DriverInput struct {
	Name       string
	LicenseNo  string
	Phone      string
	ShiftStart string
	ShiftEnd   string
}

// DriverPatch is a partial update. A nil field is unchanged; "" clears Phone and shifts.
type DriverPatch struct {
	Name       *string
	LicenseNo  *string
	Phone      *string
	ShiftStart *string
	ShiftEnd   *string
}

var shiftPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

const driverCols = `id, fleet_id, name, phone, to_char(shift_start, 'HH24:MI'), to_char(shift_end, 'HH24:MI')`

func scanDriver(r rowScanner) (Driver, error) {
	var d Driver
	var phone, start, end sql.NullString
	if err := r.Scan(&d.ID, &d.FleetID, &d.Name, &phone, &start, &end); err != nil {
		return Driver{}, err
	}
	if phone.Valid {
		d.Phone = &phone.String
	}
	if start.Valid {
		d.ShiftStart = &start.String
	}
	if end.Valid {
		d.ShiftEnd = &end.String
	}
	return d, nil
}

// optionalText maps "" to NULL and any other value to a pointer to its trimmed form.
func optionalText(s string) *string {
	if t := strings.TrimSpace(s); t != "" {
		return &t
	}
	return nil
}

// validShift accepts a nil shift (no shift) or an HH:MM value.
func validShift(s *string) error {
	if s != nil && !shiftPattern.MatchString(*s) {
		return ErrValidation
	}
	return nil
}

// CreateDriver inserts a driver. The license is normalized and hashed with the
// fleet-scoped D-31 hash; the plaintext is not stored (GF-34).
func (s *Service) CreateDriver(ctx context.Context, actor Actor, in DriverInput) (Driver, error) {
	name := strings.TrimSpace(in.Name)
	license, err := NormalizeLicense(in.LicenseNo)
	if name == "" || err != nil {
		return Driver{}, ErrValidation
	}
	phone := optionalText(in.Phone)
	start, end := optionalText(in.ShiftStart), optionalText(in.ShiftEnd)
	if err := validShift(start); err != nil {
		return Driver{}, err
	}
	if err := validShift(end); err != nil {
		return Driver{}, err
	}
	hash := LicenseHash(s.Salt, actor.FleetID, license)

	var out Driver
	err = s.write(ctx, actor, "driver.create", func(tx *sql.Tx) (string, error) {
		d, err := scanDriver(tx.QueryRowContext(ctx,
			`INSERT INTO drivers (fleet_id, name, license_no_hash, phone, shift_start, shift_end)
			 VALUES ($1, $2, $3, $4, $5::time, $6::time) RETURNING `+driverCols,
			actor.FleetID, name, hash, phone, start, end))
		if err != nil {
			return "", mapWriteErr(err)
		}
		out = d
		return driverTarget(d.ID), nil
	})
	return out, err
}

// UpdateDriver applies a patch under a row lock. A new license is re-hashed (GF-34).
func (s *Service) UpdateDriver(ctx context.Context, actor Actor, id int64, p DriverPatch) (Driver, error) {
	var hash []byte
	if p.LicenseNo != nil {
		license, err := NormalizeLicense(*p.LicenseNo)
		if err != nil {
			return Driver{}, err
		}
		hash = LicenseHash(s.Salt, actor.FleetID, license)
	}
	if p.Name != nil && strings.TrimSpace(*p.Name) == "" {
		return Driver{}, ErrValidation
	}
	var shiftStart, shiftEnd *string
	if p.ShiftStart != nil {
		shiftStart = optionalText(*p.ShiftStart)
		if err := validShift(shiftStart); err != nil {
			return Driver{}, err
		}
	}
	if p.ShiftEnd != nil {
		shiftEnd = optionalText(*p.ShiftEnd)
		if err := validShift(shiftEnd); err != nil {
			return Driver{}, err
		}
	}

	var out Driver
	err := s.write(ctx, actor, "driver.update", func(tx *sql.Tx) (string, error) {
		var cur Driver
		var currentHash []byte
		var phone, start, end sql.NullString
		err := tx.QueryRowContext(ctx,
			`SELECT id, fleet_id, name, phone, to_char(shift_start, 'HH24:MI'), to_char(shift_end, 'HH24:MI'), license_no_hash
			   FROM drivers WHERE id = $1 AND fleet_id = $2 FOR UPDATE`, id, actor.FleetID,
		).Scan(&cur.ID, &cur.FleetID, &cur.Name, &phone, &start, &end, &currentHash)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		if err != nil {
			return "", fmt.Errorf("lock driver: %w", err)
		}
		if phone.Valid {
			cur.Phone = &phone.String
		}
		if start.Valid {
			cur.ShiftStart = &start.String
		}
		if end.Valid {
			cur.ShiftEnd = &end.String
		}

		if p.Name != nil {
			cur.Name = strings.TrimSpace(*p.Name)
		}
		if p.Phone != nil {
			cur.Phone = optionalText(*p.Phone)
		}
		if p.ShiftStart != nil {
			cur.ShiftStart = shiftStart
		}
		if p.ShiftEnd != nil {
			cur.ShiftEnd = shiftEnd
		}
		if hash != nil {
			currentHash = hash
		}

		d, err := scanDriver(tx.QueryRowContext(ctx,
			`UPDATE drivers SET name = $3, license_no_hash = $4, phone = $5, shift_start = $6::time, shift_end = $7::time
			  WHERE id = $1 AND fleet_id = $2 RETURNING `+driverCols,
			id, actor.FleetID, cur.Name, currentHash, cur.Phone, cur.ShiftStart, cur.ShiftEnd))
		if err != nil {
			return "", mapWriteErr(err)
		}
		out = d
		return driverTarget(d.ID), nil
	})
	return out, err
}

// ListDrivers returns the fleet's drivers ordered by id (GF-48). A non-nil
// onlyID restricts the list to that row (driver-role scope, GF-49).
func (s *Service) ListDrivers(ctx context.Context, fleetID int64, onlyID *int64) ([]Driver, error) {
	query := `SELECT ` + driverCols + ` FROM drivers WHERE fleet_id = $1 ORDER BY id`
	args := []any{fleetID}
	if onlyID != nil {
		query = `SELECT ` + driverCols + ` FROM drivers WHERE fleet_id = $1 AND id = $2 ORDER BY id`
		args = append(args, *onlyID)
	}
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list drivers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Driver{}
	for rows.Next() {
		d, err := scanDriver(rows)
		if err != nil {
			return nil, fmt.Errorf("scan driver: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDriver returns one driver only if it belongs to the fleet (ErrNotFound otherwise).
func (s *Service) GetDriver(ctx context.Context, fleetID, id int64) (Driver, error) {
	d, err := scanDriver(s.DB.QueryRowContext(ctx,
		`SELECT `+driverCols+` FROM drivers WHERE id = $1 AND fleet_id = $2`, id, fleetID))
	if errors.Is(err, sql.ErrNoRows) {
		return Driver{}, ErrNotFound
	}
	if err != nil {
		return Driver{}, fmt.Errorf("get driver: %w", err)
	}
	return d, nil
}

func driverTarget(id int64) string {
	return fmt.Sprintf("driver:%d", id)
}
