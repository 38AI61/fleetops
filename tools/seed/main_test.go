package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"fleetops/internal/seed"
)

func TestRunUsageErrorsExitTwo(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://unused")
	cases := map[string][]string{
		"missing admin email": {},
		"bad email":           {"--admin-email", "not-an-email"},
		"bad timezone":        {"--admin-email", "a@example.com", "--timezone", "Mars/Olympus"},
		"unknown flag":        {"--password", "x"},
	}
	for name, args := range cases {
		var stderr bytes.Buffer
		if code := run(args, strings.NewReader(""), &bytes.Buffer{}, &stderr); code != 2 {
			t.Errorf("%s: exit %d, want 2 (stderr %q)", name, code, stderr.String())
		}
	}
}

func TestRunRefusesMissingDatabaseURLWithExitTwo(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	var stderr bytes.Buffer
	code := run([]string{"--admin-email", "a@example.com"}, strings.NewReader(""), &bytes.Buffer{}, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "DATABASE_URL") {
		t.Fatalf("exit %d stderr %q, want 2 naming DATABASE_URL", code, stderr.String())
	}
}

func TestReadLineSemantics(t *testing.T) {
	if got, err := readLine(strings.NewReader("correct horse battery\n")); err != nil || got != "correct horse battery" {
		t.Fatalf("LF line: %q %v", got, err)
	}
	if got, err := readLine(strings.NewReader("correct horse battery\r\n")); err != nil || got != "correct horse battery" {
		t.Fatalf("CRLF line: %q %v", got, err)
	}
	if got, err := readLine(strings.NewReader("no newline at end")); err != nil || got != "no newline at end" {
		t.Fatalf("unterminated line: %q %v", got, err)
	}
}

func TestReadLineWithoutInputIsUsageError(t *testing.T) {
	for _, in := range []string{"", "\n", "\r\n"} {
		_, err := readLine(strings.NewReader(in))
		if !errors.Is(err, seed.ErrUsage) {
			t.Errorf("stdin %q: want ErrUsage, got %v", in, err)
		}
	}
}

// TestRunDemoDevicesNeedsSalt: --demo-devices with a missing or short
// DEVICE_KEY_SALT exits 2 before any database work (GF-135).
func TestRunDemoDevicesNeedsSalt(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://unused")
	args := []string{"--admin-email", "a@example.com", "--demo-devices"}
	for name, salt := range map[string]string{"missing": "", "short": strings.Repeat("d", 31)} {
		t.Setenv("DEVICE_KEY_SALT", salt)
		var stderr bytes.Buffer
		if code := run(args, strings.NewReader(""), &bytes.Buffer{}, &stderr); code != 2 {
			t.Errorf("%s salt: exit %d, want 2 (stderr %q)", name, code, stderr.String())
		}
		if strings.Contains(stderr.String(), salt) && salt != "" {
			t.Errorf("%s salt: stderr echoes the salt", name)
		}
	}
}
