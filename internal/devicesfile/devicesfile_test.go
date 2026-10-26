package devicesfile

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fleetops/internal/devicekey"
)

// testKey returns a well-formed device key for id. The secret is fixed test data.
func testKey(id int64) string {
	return devicekey.Format(id, []byte(strings.Repeat("k", 32)))
}

func sampleFile() File {
	return File{
		Version:     Version,
		FleetID:     1,
		GeneratedAt: time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC),
		Devices: []Device{{
			DeviceID:  2,
			VehicleID: 1,
			Kind:      "tracker",
			Key:       testKey(2),
			Waypoints: []Point{{52.51, 13.34}, {52.55, 13.42}, {52.48, 13.47}, {52.58, 13.31}},
		}},
	}
}

func TestDevicesFileRoundTripAndShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	want := sampleFile()
	if err := Write(path, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Version != want.Version || got.FleetID != want.FleetID || !got.GeneratedAt.Equal(want.GeneratedAt) {
		t.Fatalf("header = %+v, want %+v", got, want)
	}
	if len(got.Devices) != 1 || got.Devices[0].DeviceID != 2 || got.Devices[0].Key != want.Devices[0].Key {
		t.Fatalf("devices = %+v", got.Devices)
	}
	if len(got.Devices[0].Waypoints) != 4 || got.Devices[0].Waypoints[2] != want.Devices[0].Waypoints[2] {
		t.Fatalf("waypoints = %+v", got.Devices[0].Waypoints)
	}

	// The on-disk shape matches GF-137 exactly.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read raw: %v", err)
	}
	var shape map[string]any
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	for _, k := range []string{"version", "fleet_id", "generated_at", "devices"} {
		if _, ok := shape[k]; !ok {
			t.Fatalf("top-level key %q missing", k)
		}
	}
	if shape["generated_at"] != "2026-10-26T10:00:00Z" {
		t.Fatalf("generated_at = %v", shape["generated_at"])
	}
	dev := shape["devices"].([]any)[0].(map[string]any)
	for _, k := range []string{"device_id", "vehicle_id", "kind", "key", "waypoints"} {
		if _, ok := dev[k]; !ok {
			t.Fatalf("device key %q missing", k)
		}
	}
	if wps := dev["waypoints"].([]any); len(wps) != 4 || wps[0].(map[string]any)["lat"] == nil {
		t.Fatalf("waypoints shape = %v", wps)
	}
}

func TestDevicesFilePerms(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deploy", ".local")
	path := filepath.Join(dir, "devices.json")
	if err := Write(path, sampleFile()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir perm = %o, want 700", dirInfo.Mode().Perm())
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("file perm = %o, want 600", fileInfo.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %d entries", len(entries))
	}
}

func TestDevicesFileMissingWrapsNotExist(t *testing.T) {
	_, err := Read(filepath.Join(t.TempDir(), "absent.json"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Read missing = %v, want wrapped fs.ErrNotExist", err)
	}
}

// TestDevicesFileRejects mutates a valid file one way at a time, marshals it,
// and checks that Read refuses it (GF-137). Errors never carry the key.
func TestDevicesFileRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(f *File)
	}{
		{"bad version", func(f *File) { f.Version = 2 }},
		{"empty devices", func(f *File) { f.Devices = nil }},
		{"duplicate device id", func(f *File) { f.Devices = append(f.Devices, f.Devices[0]) }},
		{"malformed key", func(f *File) { f.Devices[0].Key = "fo_dev_2_short" }},
		{"key id differs from device id", func(f *File) { f.Devices[0].Key = testKey(3) }},
		{"missing waypoints", func(f *File) { f.Devices[0].Waypoints = nil }},
		{"three waypoints", func(f *File) { f.Devices[0].Waypoints = f.Devices[0].Waypoints[:3] }},
		{"latitude out of range", func(f *File) { f.Devices[0].Waypoints[1].Lat = 91 }},
		{"consecutive waypoints equal", func(f *File) { f.Devices[0].Waypoints[1] = f.Devices[0].Waypoints[0] }},
		{"closing pair equal", func(f *File) { f.Devices[0].Waypoints[3] = f.Devices[0].Waypoints[0] }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := sampleFile()
			tc.mutate(&f)
			raw, err := json.Marshal(f)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			path := filepath.Join(t.TempDir(), "devices.json")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			_, err = Read(path)
			if err == nil {
				t.Fatal("Read accepted an invalid file")
			}
			if strings.Contains(err.Error(), "fo_dev_") {
				t.Fatalf("error leaks a key: %v", err)
			}
		})
	}
}
