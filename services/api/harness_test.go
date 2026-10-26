package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"fleetops/internal/auth"
	"fleetops/internal/config"
	"fleetops/internal/ingest"
	"fleetops/internal/testinfra"
	"fleetops/internal/users"
)

const testPassword = "test password long enough"

// testSecret is 32 bytes, the minimum (D-03). It is never a real secret.
var testSecret = []byte(strings.Repeat("t", 32))

// testSalt is 32 bytes, the minimum (GF-30). It is never a real secret.
var testSalt = []byte(strings.Repeat("s", 32))

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

// harness is a real NewServer on a loopback port, backed by a migrated PostGIS database.
type harness struct {
	url    string
	db     *sql.DB
	clock  *fakeClock
	issuer *auth.Issuer
	store  *users.Store
	js     jetstream.JetStream
	gate   *ingest.Gate
}

// startServer serves the real API (NewServer) over HTTP and returns the harness.
func startServer(t *testing.T) *harness {
	t.Helper()
	db := testinfra.NewMigratedDB(t)
	clk := &fakeClock{t: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	iss, err := auth.NewIssuer(testSecret, clk.now)
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	js := testinfra.NewJetStream(t)
	if err := ingest.EnsureStream(context.Background(), js); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}
	gate := ingest.NewGate()
	e := NewServer(Deps{
		DB:            db,
		Issuer:        iss,
		Clock:         clk.now,
		Throttle:      auth.NewThrottle(clk.now),
		DeviceKeySalt: testSalt,
		NATS:          js,
		Gate:          gate,
		PresenceTTL:   config.DefaultPresenceTTL,
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = e.Server.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = e.Shutdown(ctx)
	})
	return &harness{
		url:    "http://" + ln.Addr().String(),
		db:     db,
		clock:  clk,
		issuer: iss,
		store:  users.NewStore(db),
		js:     js,
		gate:   gate,
	}
}

// fleet inserts a fleet and returns its ID.
func (h *harness) fleet(t *testing.T, name string) int64 {
	t.Helper()
	var id int64
	if err := h.db.QueryRow(`INSERT INTO fleets (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert fleet: %v", err)
	}
	return id
}

// user inserts a user with the test password and returns its ID.
func (h *harness) user(t *testing.T, fleetID int64, email, role string) int64 {
	t.Helper()
	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	tx, err := h.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	u, err := h.store.Create(context.Background(), tx, fleetID, email, hash, role)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("create %s: %v", email, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return u.ID
}

// login returns an access token for the user and fails the test on any error.
func (h *harness) login(t *testing.T, email string) string {
	t.Helper()
	r := h.do(t, http.MethodPost, "/auth/login", "", `{"email":"`+email+`","password":"`+testPassword+`"}`, nil)
	if r.status != http.StatusOK {
		t.Fatalf("login %s = %d %s", email, r.status, r.body)
	}
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	tok, _ := m["access_token"].(string)
	return tok
}

type reply struct {
	status int
	body   []byte
}

// do sends one request over real HTTP. An empty bearer sends no Authorization header.
func (h *harness) do(t *testing.T, method, path, bearer, body string, cookie *http.Cookie) reply {
	t.Helper()
	req, err := http.NewRequest(method, h.url+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{status: resp.StatusCode, body: b}
}

func (r reply) errCode(t *testing.T) string {
	t.Helper()
	var m map[string]any
	_ = json.Unmarshal(r.body, &m)
	s, _ := m["error"].(string)
	return s
}

func TestServerMountsP2Routes(t *testing.T) {
	h := startServer(t)
	if r := h.do(t, http.MethodGet, "/users", "", "", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("GET /users without token = %d, want 401", r.status)
	}
	if r := h.do(t, http.MethodPost, "/auth/logout", "", "", nil); r.status != http.StatusNoContent {
		t.Fatalf("POST /auth/logout without cookie = %d, want 204", r.status)
	}
}
