package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// TestMain re-executes the test binary as a child when FLEETOPS_CLOUD_HELPER
// is set. The helper modes stand in for fleetops-* binaries (GF-349).
func TestMain(m *testing.M) {
	switch os.Getenv("FLEETOPS_CLOUD_HELPER") {
	case "exit1":
		os.Exit(1)
	case "term":
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM)
		dir := os.Getenv("FLEETOPS_CLOUD_READY")
		name := os.Getenv("FLEETOPS_CLOUD_NAME")
		//nolint:gosec // marker path under the test's own temp dir, named by the test
		if err := os.WriteFile(filepath.Join(dir, name+".ready"), nil, 0o600); err != nil {
			os.Exit(2)
		}
		<-ch
		//nolint:gosec // record path is set by the test that spawned this helper
		f, err := os.OpenFile(os.Getenv("FLEETOPS_CLOUD_RECORD"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(2)
		}
		_, _ = fmt.Fprintln(f, name)
		_ = f.Close()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestSupervisorRestartBackoff (GF-349): a child that always exits 1 is
// restarted after 1, 2, 4, 8, 16, then 30 s, and the delay stays at 30 s.
func TestSupervisorRestartBackoff(t *testing.T) {
	sup := newSupervisor()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	var got []time.Duration
	sup.sleep = func(ctx context.Context, d time.Duration) error {
		got = append(got, d)
		if calls.Add(1) == 7 {
			cancel()
			return ctx.Err()
		}
		return nil
	}
	sup.start(ctx, child{
		name: "exit1",
		path: os.Args[0],
		env:  append(os.Environ(), "FLEETOPS_CLOUD_HELPER=exit1"),
	})
	sup.wg.Wait()

	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("backoff %v, want %v", got, want)
	}
}

// TestSupervisorShutdownOrder (GF-349): children stop ws-hub, ingest, api, minio.
// Each helper records its name on SIGTERM. The record file stands in for a pipe.
func TestSupervisorShutdownOrder(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "order")
	sup := newSupervisor()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start order is minio, api, ingest, ws-hub. Shutdown must reverse it.
	names := []string{"minio", "api", "ingest", "ws-hub"}
	for _, name := range names {
		sup.start(ctx, child{
			name: name,
			path: os.Args[0],
			env: append(os.Environ(),
				"FLEETOPS_CLOUD_HELPER=term",
				"FLEETOPS_CLOUD_RECORD="+record,
				"FLEETOPS_CLOUD_NAME="+name,
				"FLEETOPS_CLOUD_READY="+dir,
			),
		})
	}
	for _, name := range names {
		waitFile(t, filepath.Join(dir, name+".ready"))
	}

	sup.shutdown()
	raw, err := os.ReadFile(record) //nolint:gosec // record lives in the test's temp dir
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	got := strings.Fields(string(raw))
	want := []string{"ws-hub", "ingest", "api", "minio"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shutdown order %v, want %v", got, want)
	}
}

// waitFile polls until path exists, for up to 10 s.
func waitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s not created within 10s", path)
}

// TestSupervisorRefusesRedisMode (GF-347, GF-349): the container refuses any
// fanout mode but nats, and the error names the variable only.
func TestSupervisorRefusesRedisMode(t *testing.T) {
	env := validBootEnv()
	env["FANOUT_MODE"] = "redis"
	_, err := bootChecks(lookupFrom(env))
	if err == nil || !strings.Contains(err.Error(), "FANOUT_MODE") {
		t.Fatalf("err %v, want FANOUT_MODE refusal", err)
	}
	if strings.Contains(err.Error(), "redis") {
		t.Fatalf("err %q echoes the value", err)
	}
}

// TestSupervisorRequiresMinIOCreds (GF-350): short MinIO credentials are refused,
// and the error names the variable and not the value.
func TestSupervisorRequiresMinIOCreds(t *testing.T) {
	cases := map[string]string{"MINIO_KEY": "ab", "MINIO_SECRET": "short"}
	for name, value := range cases {
		env := validBootEnv()
		env[name] = value
		_, err := bootChecks(lookupFrom(env))
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("%s=%d bytes: err %v, want refusal naming %s", name, len(value), err, name)
		}
		if strings.Contains(err.Error(), value) {
			t.Fatalf("%s: error echoes the value", name)
		}
	}
}

// TestBootChecksPort covers the PORT default and range.
func TestBootChecksPort(t *testing.T) {
	env := validBootEnv()
	if port, err := bootChecks(lookupFrom(env)); err != nil || port != defaultPort {
		t.Fatalf("default port %d err %v", port, err)
	}
	env["PORT"] = "70000"
	if _, err := bootChecks(lookupFrom(env)); err == nil || !strings.Contains(err.Error(), "PORT") {
		t.Fatalf("err %v, want PORT range refusal", err)
	}
}

// TestChildEnvOverrides (GF-349): children never see REDIS_URL, and they get
// the container's NATS, MinIO and fanout settings.
func TestChildEnvOverrides(t *testing.T) {
	got := childEnv([]string{"REDIS_URL=redis://x:6379", "NATS_URL=nats://elsewhere", "JWT_SECRET=j"})
	joined := strings.Join(got, "\n")
	for _, must := range []string{"NATS_URL=" + natsURL, "MINIO_ENDPOINT=" + minioAddr, "FANOUT_MODE=nats", "JWT_SECRET=j"} {
		if !strings.Contains(joined, must) {
			t.Errorf("child env missing %s", must)
		}
	}
	for _, bad := range []string{"REDIS_URL=", "elsewhere"} {
		if strings.Contains(joined, bad) {
			t.Errorf("child env still carries %s", bad)
		}
	}
}

// TestWaitMinIOReady (GF-350): readiness passes once the health endpoint
// answers 200, and times out when it never does.
func TestWaitMinIOReady(t *testing.T) {
	var hits atomic.Int32
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ready.Close)
	if err := waitMinIOReady(context.Background(), ready.URL, 10*time.Second); err != nil {
		t.Fatalf("ready after retries: %v", err)
	}

	never := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(never.Close)
	if err := waitMinIOReady(context.Background(), never.URL, 400*time.Millisecond); err == nil {
		t.Fatal("never-ready server reported ready")
	}
}

// TestEmbeddedNATSJetStreamReady (GF-350, D-16): the embedded server starts with
// JetStream on, and a nats.go client reads JetStream account info.
func TestEmbeddedNATSJetStreamReady(t *testing.T) {
	srv, err := startNATS("127.0.0.1", -1, t.TempDir())
	if err != nil {
		t.Fatalf("start embedded nats: %v", err)
	}
	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := js.AccountInfo(ctx); err != nil {
		t.Fatalf("jetstream account info: %v", err)
	}
}

// validBootEnv is a boot environment that passes bootChecks.
func validBootEnv() map[string]string {
	return map[string]string{
		"FANOUT_MODE":     "nats",
		"DATABASE_URL":    "postgres://fleetops@db.invalid/fleetops",
		"JWT_SECRET":      strings.Repeat("j", 32),
		"DEVICE_KEY_SALT": strings.Repeat("d", 32),
		"MINIO_KEY":       "minio-key",
		"MINIO_SECRET":    "minio-secret-value",
	}
}

func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}
}
