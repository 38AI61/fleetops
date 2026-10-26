package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"fleetops/internal/devicekey"
	"fleetops/internal/devicesfile"
)

// writeDevices writes a valid four-device file and returns its path.
func writeDevices(t *testing.T) string {
	t.Helper()
	secret := []byte(strings.Repeat("k", 32))
	wps := []devicesfile.Point{
		{Lat: 52.51, Lng: 13.34}, {Lat: 52.55, Lng: 13.42}, {Lat: 52.48, Lng: 13.47}, {Lat: 52.58, Lng: 13.31},
	}
	f := devicesfile.File{Version: devicesfile.Version, FleetID: 1, Devices: nil}
	for id := int64(2); id <= 5; id++ {
		f.Devices = append(f.Devices, devicesfile.Device{
			DeviceID: id, VehicleID: id - 1, Kind: "tracker",
			Key: devicekey.Format(id, secret), Waypoints: wps,
		})
	}
	path := filepath.Join(t.TempDir(), "devices.json")
	if err := devicesfile.Write(path, f); err != nil {
		t.Fatalf("write devices: %v", err)
	}
	return path
}

// TestSimulatorFlagBounds: each bad flag, bound, or URL exits 2 (GF-141).
func TestSimulatorFlagBounds(t *testing.T) {
	path := writeDevices(t)
	cases := map[string][]string{
		"rate below 500ms":    {"--rate=499ms"},
		"rate above 60s":      {"--rate=61s"},
		"fleet zero":          {"--fleet=0"},
		"fleet above devices": {"--fleet=5"},
		"api without scheme":  {"--api=127.0.0.1:10010"},
		"api wrong scheme":    {"--api=ftp://127.0.0.1:10010"},
		"api without host":    {"--api=http://"},
		"positional argument": {"stray"},
	}
	for name, args := range cases {
		var stderr bytes.Buffer
		all := append([]string{"--devices=" + path}, args...)
		if code := run(all, &stderr); code != 2 {
			t.Errorf("%s: exit %d, want 2 (stderr %q)", name, code, stderr.String())
		}
	}
}

// TestSimulatorDevicesMissing: a missing keys file exits 1 and points at --demo-devices (GF-141).
func TestSimulatorDevicesMissing(t *testing.T) {
	var stderr bytes.Buffer
	missing := filepath.Join(t.TempDir(), "absent.json")
	code := run([]string{"--devices=" + missing, "--fleet=1"}, &stderr)
	if code != 1 {
		t.Fatalf("exit %d, want 1 (stderr %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--demo-devices") {
		t.Fatalf("stderr %q does not point to --demo-devices", stderr.String())
	}
}
