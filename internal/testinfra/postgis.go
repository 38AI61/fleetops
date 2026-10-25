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

	password, err := randomPassword()
	if err != nil {
		t.Fatalf("generate postgis password: %v", err)
	}

	ctx := context.Background()
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

	dsn, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("build DSN for %s: %v", PostgisImage, err)
	}
	return dsn
}

func randomPassword() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
