package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	fleetdb "fleetops/db"
	"fleetops/internal/seed"
	"fleetops/internal/testinfra"
)

// cliAdminPW is a test fixture for the admin password, never a real secret.
const cliAdminPW = "correct horse battery"

// migratedDSN starts a PostGIS container and applies every migration, so the
// CLI can run against it (GF-345). The container is torn down with the test.
func migratedDSN(t *testing.T) string {
	t.Helper()
	dsn := testinfra.StartPostgis(t)
	src, err := iofs.New(fleetdb.Migrations, "migrations")
	if err != nil {
		t.Fatalf("migration source: %v", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	return dsn
}

// runSeedDemo runs the CLI with --demo-devices and the given stdin, and returns
// the exit code and both output streams.
func runSeedDemo(stdin string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run([]string{"--admin-email", "admin@example.com", "--demo-devices"}, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

// setupCLI points the CLI at a fresh migrated database and a temp working
// directory, so the default devices and demo-users paths land in the temp dir.
func setupCLI(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", migratedDSN(t))
	t.Setenv("DEVICE_KEY_SALT", strings.Repeat("d", 32))
	t.Chdir(t.TempDir())
}

// TestSeedDemoTargetExitsZeroOnRerun: the `make seed-demo` command exits 0 on a
// seeded database, never prompts, and rewrites neither demo file (GF-340).
func TestSeedDemoTargetExitsZeroOnRerun(t *testing.T) {
	setupCLI(t)

	if code, _, errOut := runSeedDemo(cliAdminPW + "\n"); code != 0 {
		t.Fatalf("first seed exit %d, stderr %q", code, errOut)
	}
	devicesBefore := readFile(t, "deploy/.local/devices.json")
	usersBefore := readFile(t, seed.DemoUsersDefaultPath)

	// An empty stdin proves the rerun never reads the password (no prompt).
	code, stdout, _ := runSeedDemo("")
	if code != 0 {
		t.Fatalf("rerun exit %d, want 0", code)
	}
	if !strings.Contains(stdout, "already seeded; no changes") {
		t.Fatalf("rerun stdout %q, want the already-seeded line", stdout)
	}
	if !bytes.Equal(devicesBefore, readFile(t, "deploy/.local/devices.json")) || !bytes.Equal(usersBefore, readFile(t, seed.DemoUsersDefaultPath)) {
		t.Fatal("rerun rewrote a demo file")
	}
}

// TestDemoPasswordsNotLogged: no demo driver password and no admin password
// appears on stdout or stderr of the first seed (GF-23, GF-339).
func TestDemoPasswordsNotLogged(t *testing.T) {
	setupCLI(t)

	code, stdout, stderr := runSeedDemo(cliAdminPW + "\n")
	if code != 0 {
		t.Fatalf("first seed exit %d, stderr %q", code, stderr)
	}
	var f seed.DemoUsersFile
	if err := json.Unmarshal(readFile(t, seed.DemoUsersDefaultPath), &f); err != nil {
		t.Fatalf("decode demo users: %v", err)
	}
	if len(f.Drivers) != seed.DemoDriverCount {
		t.Fatalf("drivers = %d, want %d", len(f.Drivers), seed.DemoDriverCount)
	}
	output := stdout + stderr
	if strings.Contains(output, cliAdminPW) {
		t.Fatal("admin password was printed")
	}
	for _, d := range f.Drivers {
		if strings.Contains(output, d.Password) {
			t.Fatalf("driver %s password was printed", d.Email)
		}
	}
}

// readFile reads a file relative to the working directory the test set up.
func readFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return body
}
