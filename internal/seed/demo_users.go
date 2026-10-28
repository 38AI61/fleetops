package seed

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// DemoDriverCount is the number of demo drivers, one per demo van (GF-339, GF-318).
const DemoDriverCount = 50

// DemoUsersVersion is the only demo-users schema version this code writes.
const DemoUsersVersion = 1

// DemoUsersDefaultPath is the gitignored driver-login file (GF-339).
const DemoUsersDefaultPath = "deploy/.local/demo-users.json"

// demoPasswordLen is the generated driver password length (GF-339).
const demoPasswordLen = 24

// demoPasswordAlphabet has no look-alike characters (0/O, 1/l/I) so a typed password is safe.
const demoPasswordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"

// DemoUsersFile is deploy/.local/demo-users.json (GF-339). It holds passwords, so it is 0600.
type DemoUsersFile struct {
	Version     int          `json:"version"`
	FleetID     int64        `json:"fleet_id"`
	GeneratedAt time.Time    `json:"generated_at"`
	Drivers     []DemoDriver `json:"drivers"`
}

// DemoDriver is one demo driver login and its van.
type DemoDriver struct {
	DriverID  int64  `json:"driver_id"`
	UserID    int64  `json:"user_id"`
	Email     string `json:"email"`
	Password  string `json:"password"`
	VehicleID int64  `json:"vehicle_id"`
}

// demoEmail is the reserved-domain login for driver i (GF-339).
func demoEmail(i int) string {
	return fmt.Sprintf("demo-driver-%03d@example.com", i)
}

// newDemoPassword returns a random 24-character password. It is checked against
// the policy by the caller before any hash or write (GF-23, GF-339).
func newDemoPassword() (string, error) {
	out := make([]byte, demoPasswordLen)
	max := big.NewInt(int64(len(demoPasswordAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("demo password: %w", err)
		}
		out[i] = demoPasswordAlphabet[n.Int64()]
	}
	return string(out), nil
}

// writeDemoUsers writes f atomically, the devicesfile pattern (GF-138, GF-339):
// a temp file in the same directory (created 0600), fsync, then rename. The
// directory is created 0700 when missing.
func writeDemoUsers(path string, f DemoUsersFile) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	body, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode demo users file: %w", err)
	}
	body = append(body, '\n')

	tmp, err := os.CreateTemp(dir, ".demo-users-*.tmp") // O_EXCL, mode 0600
	if err != nil {
		return fmt.Errorf("create temp demo users file: %w", err)
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
		return fmt.Errorf("write demo users file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync demo users file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close demo users file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename demo users file: %w", err)
	}
	committed = true
	return nil
}
