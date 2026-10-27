// Package prooftest runs one MinIO container per test binary for the proofs store
// and the services/api harness (GF-203). It mirrors internal/testinfra/nats.go.
package prooftest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/testcontainers/testcontainers-go"
	miniomodule "github.com/testcontainers/testcontainers-go/modules/minio"

	"fleetops/internal/proofs"
)

// Image is the MinIO server image for tests. Compose uses the same tag (GF-205).
const Image = "bitnamilegacy/minio:2025.7.23-debian-12-r5"

// rootUser and rootPassword are the local-dev defaults, never a real secret.
const (
	rootUser     = "minioadmin" //nolint:gosec // local test container default, not a credential
	rootPassword = "minioadmin" //nolint:gosec // local test container default, not a credential
)

// shared holds the one MinIO container a test process uses.
var shared struct {
	once      sync.Once
	ctr       *miniomodule.MinioContainer
	endpoint  string
	store     *proofs.Store
	err       error
	terminate sync.Once
}

// start runs the container on first use, then ensures the proofs bucket the
// way API boot does.
func start() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	shared.ctr, shared.err = miniomodule.Run(ctx, Image,
		miniomodule.WithUsername(rootUser), miniomodule.WithPassword(rootPassword),
		// Bitnami's default user 1001 cannot write the data paths (GF-208).
		testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{User: "root"}}))
	if shared.err != nil {
		return
	}
	if shared.endpoint, shared.err = shared.ctr.ConnectionString(ctx); shared.err != nil {
		return
	}
	env := map[string]string{
		"MINIO_ENDPOINT": shared.endpoint,
		"MINIO_KEY":      rootUser,
		"MINIO_SECRET":   rootPassword,
	}
	if shared.store, shared.err = proofs.FromEnv(func(k string) string { return env[k] }); shared.err != nil {
		return
	}
	shared.err = shared.store.EnsureBucket(ctx)
}

// SharedStore returns a Store on the shared container, with the proofs bucket
// ensured. Each call returns its own copy, so a test may change PresignTTL. A
// startup failure is fatal for t.
func SharedStore(t testing.TB) *proofs.Store {
	t.Helper()
	shared.once.Do(start)
	if shared.err != nil {
		t.Fatalf("start %s (is Docker running?): %v", Image, shared.err)
	}
	st := *shared.store
	return &st
}

// Client returns a raw minio client on the shared container, for test assertions
// such as listing and heading objects.
func Client(t testing.TB) *minio.Client {
	t.Helper()
	SharedStore(t)
	c, err := minio.New(shared.endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(rootUser, rootPassword, ""),
		Region: "us-east-1",
	})
	if err != nil {
		t.Fatalf("minio client: %v", err)
	}
	return c
}

// TerminateMinIO removes the shared MinIO container. Call it from TestMain after
// m.Run so the container does not outlive the test process.
func TerminateMinIO() {
	shared.terminate.Do(func() {
		if shared.ctr == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = shared.ctr.Terminate(ctx)
	})
}
