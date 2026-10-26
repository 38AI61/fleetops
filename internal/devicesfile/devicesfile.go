// Package devicesfile is the single definition of deploy/.local/devices.json
// (D-12, GF-137). tools/seed writes it and the simulator reads it. The file
// holds plaintext device keys, so it is written 0600 and never printed (GF-138).
package devicesfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"fleetops/internal/devicekey"
)

// Version is the only schema version this package reads or writes (GF-137).
const Version = 1

// DefaultPath is the gitignored keys file, relative to the working directory (GF-138).
const DefaultPath = "deploy/.local/devices.json"

// WaypointsPerDevice is the length of every route loop (GF-142).
const WaypointsPerDevice = 4

// Point is one waypoint in degrees.
type Point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// Device is one tracker entry. Key is the plaintext device key, the only copy.
type Device struct {
	DeviceID  int64   `json:"device_id"`
	VehicleID int64   `json:"vehicle_id"`
	Kind      string  `json:"kind"`
	Key       string  `json:"key"`
	Waypoints []Point `json:"waypoints"`
}

// File is the whole keys file. FleetID is for operators; the simulator does not use it.
type File struct {
	Version     int       `json:"version"`
	FleetID     int64     `json:"fleet_id"`
	GeneratedAt time.Time `json:"generated_at"`
	Devices     []Device  `json:"devices"`
}

// Validate applies the GF-137 rules. Errors name the device id, never the key.
func Validate(f File) error {
	if f.Version != Version {
		return fmt.Errorf("devices file: version %d, want %d", f.Version, Version)
	}
	if len(f.Devices) == 0 {
		return errors.New("devices file: no devices")
	}
	seen := make(map[int64]bool, len(f.Devices))
	for _, d := range f.Devices {
		if seen[d.DeviceID] {
			return fmt.Errorf("devices file: duplicate device_id %d", d.DeviceID)
		}
		seen[d.DeviceID] = true

		id, _, err := devicekey.Parse(d.Key)
		if err != nil {
			return fmt.Errorf("devices file: device %d: key is malformed", d.DeviceID)
		}
		if id != d.DeviceID {
			return fmt.Errorf("devices file: device %d: key belongs to device %d", d.DeviceID, id)
		}
		if err := validateWaypoints(d.Waypoints); err != nil {
			return fmt.Errorf("devices file: device %d: %w", d.DeviceID, err)
		}
	}
	return nil
}

// validateWaypoints requires exactly four in-range points, and no two consecutive
// points equal, including the closing pair w3 to w0 (GF-142).
func validateWaypoints(wps []Point) error {
	if len(wps) != WaypointsPerDevice {
		return fmt.Errorf("waypoints: want %d, got %d", WaypointsPerDevice, len(wps))
	}
	for _, p := range wps {
		// The negated form also rejects NaN.
		if !(p.Lat >= -90 && p.Lat <= 90) || !(p.Lng >= -180 && p.Lng <= 180) {
			return errors.New("waypoints: point out of range")
		}
	}
	for i := range wps {
		if wps[i] == wps[(i+1)%len(wps)] {
			return errors.New("waypoints: consecutive points are equal")
		}
	}
	return nil
}

// Write validates f and writes it atomically: a temp file in the same
// directory, mode 0600, fsync, then rename (GF-138). The directory is created
// 0700 when missing.
func Write(path string, f File) error {
	if err := Validate(f); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	body, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode devices file: %w", err)
	}
	body = append(body, '\n')

	tmp, err := os.CreateTemp(dir, ".devices-*.tmp") // O_EXCL, mode 0600
	if err != nil {
		return fmt.Errorf("create temp devices file: %w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write devices file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync devices file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close devices file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename devices file: %w", err)
	}
	committed = true
	return nil
}

// Read parses and validates the keys file. A missing file returns an error
// that wraps fs.ErrNotExist, so callers can point the operator at --demo-devices.
func Read(path string) (File, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return File{}, fmt.Errorf("read devices file: %w", err)
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return File{}, errors.New("devices file: not valid JSON")
	}
	if err := Validate(f); err != nil {
		return File{}, err
	}
	return f, nil
}
