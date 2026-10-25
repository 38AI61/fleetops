// Package testinfra starts real infrastructure for tests. Tests never mock
// the database; they run against the PostGIS container from this package.
package testinfra

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// PostgisImage is the single PostGIS image used by tests and CI (GF-12).
const PostgisImage = "postgis/postgis:16-3.4"

// StartPostgis starts a PostGIS container with a per-run random password and
// returns a DSN for database fleetops. The container is terminated when the
// test finishes.
func StartPostgis(t testing.TB) (dsn string) {
	t.Helper()

	ctr, dsn, err := startPostgis(context.Background())
	if ctr != nil {
		t.Cleanup(func() {
			if err := testcontainers.TerminateContainer(ctr); err != nil {
				t.Logf("terminate %s: %v", PostgisImage, err)
			}
		})
	}
	if err != nil {
		t.Fatalf("start %s (is Docker running?): %v", PostgisImage, err)
	}
	return dsn
}

// startPostgis runs the container and returns it with a DSN for database
// fleetops. The container may be non-nil even when err is set, so callers can
// clean it up.
func startPostgis(ctx context.Context) (testcontainers.Container, string, error) {
	password, err := randomHex(16)
	if err != nil {
		return nil, "", err
	}

	ctr, err := postgres.Run(ctx, PostgisImage,
		postgres.WithDatabase("fleetops"),
		postgres.WithUsername("fleetops"),
		postgres.WithPassword(password),
		// The first "ready" line comes from initdb's temporary server; the
		// second comes from the real one, which is what we connect to.
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		return ctr, "", err
	}

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return ctr, "", err
	}
	return ctr, dsn, nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
