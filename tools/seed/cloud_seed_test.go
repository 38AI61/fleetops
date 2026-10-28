package main

import (
	"bytes"
	"database/sql"
	"os"
	"strings"
	"testing"

	"fleetops/internal/auth"
)

// cloudAdminPW is a test fixture for the cloud seed password, never a real secret.
const cloudAdminPW = "cloud horse battery staple"

// cloudRun runs the CLI with --password-from-env, the cloud seed path (GF-351).
func cloudRun(t *testing.T, stdin string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run([]string{"--admin-email", "admin@example.com", "--demo-devices", "--password-from-env"},
		strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

// storedHash returns the admin's password hash, so tests can check which
// password was stored without logging in.
func storedHash(t *testing.T, dsn string) string {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	var hash string
	if err := db.QueryRow(`SELECT password_hash FROM users WHERE email = 'admin@example.com'`).Scan(&hash); err != nil {
		t.Fatalf("read admin hash: %v", err)
	}
	return hash
}

// TestPasswordFromEnvSeedsAndExitsZero (GF-351): with the flag and SEED_PASSWORD,
// the first seed needs no terminal and no stdin.
func TestPasswordFromEnvSeedsAndExitsZero(t *testing.T) {
	setupCLI(t)
	t.Setenv("SEED_PASSWORD", cloudAdminPW)
	code, stdout, stderr := cloudRun(t, "")
	if code != 0 || !strings.Contains(stdout, "seeded fleet") {
		t.Fatalf("exit %d stdout %q stderr %q, want 0 seeded", code, stdout, stderr)
	}
	if ok, err := auth.VerifyPassword(storedHash(t, os.Getenv("DATABASE_URL")), cloudAdminPW); err != nil || !ok {
		t.Fatalf("stored admin password does not match SEED_PASSWORD: %v", err)
	}
}

// TestPasswordFromEnvMissingExitsTwo (GF-351): an unset SEED_PASSWORD is a usage
// error. The message names the variable and not a value.
func TestPasswordFromEnvMissingExitsTwo(t *testing.T) {
	setupCLI(t)
	t.Setenv("SEED_PASSWORD", "")
	code, _, stderr := cloudRun(t, "")
	if code != 2 || !strings.Contains(stderr, "SEED_PASSWORD") {
		t.Fatalf("exit %d stderr %q, want 2 naming SEED_PASSWORD", code, stderr)
	}
}

// TestPasswordFromEnvWeakExitsTwo (GF-351): the password policy is unchanged, so
// a weak SEED_PASSWORD is refused with exit 2 before any write.
func TestPasswordFromEnvWeakExitsTwo(t *testing.T) {
	setupCLI(t)
	t.Setenv("SEED_PASSWORD", "short")
	code, _, stderr := cloudRun(t, "")
	if code != 2 {
		t.Fatalf("exit %d stderr %q, want 2", code, stderr)
	}
	if strings.Contains(stderr, "short") {
		t.Fatalf("stderr echoes the weak password: %q", stderr)
	}
}

// TestPasswordFromEnvRerunExitsZeroWithoutEnv (GF-340, GF-351): a rerun on a seeded
// database exits 0 with no SEED_PASSWORD, because the check runs before any read.
func TestPasswordFromEnvRerunExitsZeroWithoutEnv(t *testing.T) {
	setupCLI(t)
	t.Setenv("SEED_PASSWORD", cloudAdminPW)
	if code, _, stderr := cloudRun(t, ""); code != 0 {
		t.Fatalf("first seed exit %d stderr %q", code, stderr)
	}
	t.Setenv("SEED_PASSWORD", "")
	code, stdout, stderr := cloudRun(t, "")
	if code != 0 || !strings.Contains(stdout, "already seeded; no changes") {
		t.Fatalf("rerun exit %d stdout %q stderr %q, want 0 already seeded", code, stdout, stderr)
	}
}

// TestEnvIgnoredWithoutFlag (GF-351): without the flag SEED_PASSWORD is never
// read. The stdin password is the one that is stored.
func TestEnvIgnoredWithoutFlag(t *testing.T) {
	setupCLI(t)
	t.Setenv("SEED_PASSWORD", cloudAdminPW)
	var out, errOut bytes.Buffer
	code := run([]string{"--admin-email", "admin@example.com", "--demo-devices"},
		strings.NewReader(cliAdminPW+"\n"), &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, errOut.String())
	}
	hash := storedHash(t, os.Getenv("DATABASE_URL"))
	if ok, _ := auth.VerifyPassword(hash, cliAdminPW); !ok {
		t.Fatal("stdin password is not the stored one")
	}
	if ok, _ := auth.VerifyPassword(hash, cloudAdminPW); ok {
		t.Fatal("SEED_PASSWORD was used without the flag")
	}
}

// TestSeedPasswordNeverLogged (GF-351): the password appears in no output stream.
func TestSeedPasswordNeverLogged(t *testing.T) {
	setupCLI(t)
	t.Setenv("SEED_PASSWORD", cloudAdminPW)
	code, stdout, stderr := cloudRun(t, "")
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	if strings.Contains(stdout, cloudAdminPW) || strings.Contains(stderr, cloudAdminPW) {
		t.Fatal("seed output contains the SEED_PASSWORD value")
	}
}
