package users_test

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

	"github.com/labstack/echo/v4"

	"fleetops/internal/auth"
	"fleetops/internal/testinfra"
	"fleetops/internal/users"
)

const adminPW = "admin password long"

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

type env struct {
	url   string
	db    *sql.DB
	store *users.Store
	clk   *clock
}

// startServer mounts the auth and users routes exactly as services/api will,
// over real HTTP on a loopback port.
func startServer(t *testing.T) *env {
	t.Helper()
	db := testinfra.NewMigratedDB(t)
	clk := &clock{t: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	iss, err := auth.NewIssuer([]byte(strings.Repeat("s", 32)), clk.now)
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	store := users.NewStore(db)
	ah := &auth.Handlers{DB: db, Issuer: iss, Throttle: auth.NewThrottle(clk.now), Accounts: store, Clock: clk.now}
	uh := &users.Handler{DB: db, Store: store, Clock: clk.now}

	e := echo.New()
	e.HideBanner = true
	e.POST("/auth/login", ah.Login)
	e.POST("/auth/refresh", ah.Refresh)
	admin := []echo.MiddlewareFunc{auth.RequireAuth(iss), auth.RequireRole("admin")}
	e.GET("/users", uh.List, admin...)
	e.POST("/users", uh.Create, admin...)
	e.PATCH("/users/:id", uh.Update, admin...)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = e.Server.Serve(ln) }()
	t.Cleanup(func() { _ = e.Close() })
	return &env{url: "http://" + ln.Addr().String(), db: db, store: store, clk: clk}
}

// fleetWith creates a fleet and one admin in it.
func (e *env) fleetWith(t *testing.T, name, adminEmail string) (fleetID, adminID int64) {
	t.Helper()
	if err := e.db.QueryRow(`INSERT INTO fleets (name) VALUES ($1) RETURNING id`, name).Scan(&fleetID); err != nil {
		t.Fatalf("insert fleet: %v", err)
	}
	adminID = e.addUser(t, fleetID, adminEmail, "admin", adminPW).ID
	return fleetID, adminID
}

func (e *env) addUser(t *testing.T, fleetID int64, email, role, pw string) users.User {
	t.Helper()
	hash, err := auth.HashPassword(pw)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	tx, err := e.store.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	u, err := e.store.Create(context.Background(), tx, fleetID, email, hash, role)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("create %s: %v", email, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return u
}

type resp struct {
	status int
	body   []byte
	cookie *http.Cookie
}

func (r resp) errCode(t *testing.T) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("decode %q: %v", r.body, err)
	}
	s, _ := m["error"].(string)
	return s
}

func (e *env) send(t *testing.T, method, path, bearer, body string, cookie *http.Cookie) resp {
	t.Helper()
	req, err := http.NewRequest(method, e.url+path, strings.NewReader(body))
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
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	r := resp{status: res.StatusCode, body: b}
	for _, c := range res.Cookies() {
		if c.Name == auth.RefreshCookieName {
			r.cookie = c
		}
	}
	return r
}

// login returns the access token and refresh cookie for a user.
func (e *env) login(t *testing.T, email, pw string) (string, *http.Cookie) {
	t.Helper()
	r := e.send(t, http.MethodPost, "/auth/login", "", `{"email":"`+email+`","password":"`+pw+`"}`, nil)
	if r.status != http.StatusOK {
		t.Fatalf("login %s = %d %s", email, r.status, r.body)
	}
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	tok, _ := m["access_token"].(string)
	return tok, r.cookie
}

func (e *env) auditCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM admin_audit`).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	return n
}

func TestAdminCreateListPatchHappyPath(t *testing.T) {
	e := startServer(t)
	_, _ = e.fleetWith(t, "A", "admin@a.example")
	tok, _ := e.login(t, "admin@a.example", adminPW)

	created := e.send(t, http.MethodPost, "/users", tok,
		`{"email":"Mgr@A.example","password":"manager password","role":"manager"}`, nil)
	if created.status != http.StatusCreated {
		t.Fatalf("POST = %d %s", created.status, created.body)
	}
	if strings.Contains(string(created.body), "password") {
		t.Fatalf("response leaks password field: %s", created.body)
	}

	list := e.send(t, http.MethodGet, "/users", tok, "", nil)
	if list.status != http.StatusOK || strings.Contains(string(list.body), "password_hash") {
		t.Fatalf("GET = %d %s", list.status, list.body)
	}
	if !strings.Contains(string(list.body), "mgr@a.example") {
		t.Fatalf("GET missing created user: %s", list.body)
	}

	var m map[string]any
	_ = json.Unmarshal(created.body, &m)
	id := m["id"].(float64)
	patch := e.send(t, http.MethodPatch, "/users/"+jsonNum(id), tok, `{"role":"dispatcher"}`, nil)
	if patch.status != http.StatusOK || !strings.Contains(string(patch.body), `"role":"dispatcher"`) {
		t.Fatalf("PATCH = %d %s", patch.status, patch.body)
	}
}

func TestAuditRowPerSuccessfulWriteWithActor(t *testing.T) {
	e := startServer(t)
	fleetID, adminID := e.fleetWith(t, "A", "admin@a.example")
	tok, _ := e.login(t, "admin@a.example", adminPW)

	created := e.send(t, http.MethodPost, "/users", tok,
		`{"email":"d@a.example","password":"dispatcher pw long","role":"dispatcher"}`, nil)
	if created.status != http.StatusCreated {
		t.Fatalf("POST = %d", created.status)
	}
	var m map[string]any
	_ = json.Unmarshal(created.body, &m)
	newID := jsonNum(m["id"].(float64))

	if r := e.send(t, http.MethodPatch, "/users/"+newID, tok, `{"role":"driver"}`, nil); r.status != http.StatusOK {
		t.Fatalf("PATCH role = %d", r.status)
	}
	if r := e.send(t, http.MethodPatch, "/users/"+newID, tok, `{"password":"new dispatcher pw"}`, nil); r.status != http.StatusOK {
		t.Fatalf("PATCH password = %d", r.status)
	}

	rows, err := e.db.Query(`SELECT fleet_id, actor_id, action, target FROM admin_audit ORDER BY id`)
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	defer func() { _ = rows.Close() }()
	want := []struct{ action string }{{"user.create"}, {"user.update"}, {"user.password_reset"}}
	i := 0
	for rows.Next() {
		var fid, actor int64
		var action, target string
		if err := rows.Scan(&fid, &actor, &action, &target); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if i >= len(want) || action != want[i].action {
			t.Fatalf("row %d action = %q", i, action)
		}
		if fid != fleetID || actor != adminID || target != "user:"+newID {
			t.Fatalf("row %d = fleet %d actor %d target %q", i, fid, actor, target)
		}
		i++
	}
	if i != len(want) {
		t.Fatalf("audit rows = %d, want %d", i, len(want))
	}
}

func TestFailedWritesWriteNoAuditRow(t *testing.T) {
	e := startServer(t)
	_, _ = e.fleetWith(t, "A", "admin@a.example")
	fleetB, _ := e.fleetWith(t, "B", "admin@b.example")
	tok, _ := e.login(t, "admin@a.example", adminPW)
	otherFleet := e.addUser(t, fleetB, "victim@b.example", "driver", adminPW)
	before := e.auditCount(t)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
		code   string
	}{
		{"duplicate email 409", http.MethodPost, "/users",
			`{"email":"ADMIN@a.example","password":"another long pw","role":"manager"}`, 409, "conflict"},
		{"short password 400", http.MethodPost, "/users",
			`{"email":"x@a.example","password":"short","role":"manager"}`, 400, "weak_password"},
		{"bad role 400", http.MethodPost, "/users",
			`{"email":"x@a.example","password":"long enough pw","role":"root"}`, 400, "validation_failed"},
		{"patch unknown 404", http.MethodPatch, "/users/999999", `{"role":"manager"}`, 404, "not_found"},
		{"patch other fleet 404", http.MethodPatch, "/users/" + jsonNum(float64(otherFleet.ID)), `{"role":"manager"}`, 404, "not_found"},
		{"patch weak reset 400", http.MethodPatch, "/users/" + jsonNum(float64(otherFleet.ID)), `{"password":"short"}`, 400, "weak_password"},
	}
	for _, tc := range cases {
		r := e.send(t, tc.method, tc.path, tok, tc.body, nil)
		if r.status != tc.want || r.errCode(t) != tc.code {
			t.Errorf("%s: got %d %s, want %d %s", tc.name, r.status, r.body, tc.want, tc.code)
		}
	}
	if after := e.auditCount(t); after != before {
		t.Fatalf("failed writes added %d audit rows, want 0", after-before)
	}
}

func TestDisableRevokesRefreshTokens(t *testing.T) {
	e := startServer(t)
	_, _ = e.fleetWith(t, "A", "admin@a.example")
	adminTok, _ := e.login(t, "admin@a.example", adminPW)
	mgr := e.addUser(t, 1, "mgr@a.example", "manager", "manager password")
	_, cookie := e.login(t, "mgr@a.example", "manager password")

	if r := e.send(t, http.MethodPatch, "/users/"+jsonNum(float64(mgr.ID)), adminTok, `{"disabled":true}`, nil); r.status != http.StatusOK {
		t.Fatalf("disable = %d %s", r.status, r.body)
	}
	var live int
	if err := e.db.QueryRow(`SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND revoked_at IS NULL`,
		mgr.ID).Scan(&live); err != nil {
		t.Fatalf("count: %v", err)
	}
	if live != 0 {
		t.Fatalf("live tokens after disable = %d, want 0", live)
	}
	if r := e.send(t, http.MethodPost, "/auth/refresh", "", "", cookie); r.status != http.StatusUnauthorized {
		t.Fatalf("refresh after disable = %d, want 401", r.status)
	}
}

func TestPasswordResetRevokesRefreshTokens(t *testing.T) {
	e := startServer(t)
	_, _ = e.fleetWith(t, "A", "admin@a.example")
	adminTok, _ := e.login(t, "admin@a.example", adminPW)
	mgr := e.addUser(t, 1, "mgr@a.example", "manager", "manager password")
	_, cookie := e.login(t, "mgr@a.example", "manager password")

	if r := e.send(t, http.MethodPatch, "/users/"+jsonNum(float64(mgr.ID)), adminTok,
		`{"password":"reset manager password"}`, nil); r.status != http.StatusOK {
		t.Fatalf("reset = %d %s", r.status, r.body)
	}
	if r := e.send(t, http.MethodPost, "/auth/refresh", "", "", cookie); r.status != http.StatusUnauthorized {
		t.Fatalf("refresh after reset = %d, want 401", r.status)
	}
	if r := e.send(t, http.MethodPost, "/auth/login", "", `{"email":"mgr@a.example","password":"manager password"}`, nil); r.status != http.StatusUnauthorized {
		t.Fatalf("old password after reset = %d, want 401", r.status)
	}
	e.login(t, "mgr@a.example", "reset manager password")
}

func TestSelfDemoteAndSelfDisableAreConflicts(t *testing.T) {
	e := startServer(t)
	_, adminID := e.fleetWith(t, "A", "admin@a.example")
	tok, _ := e.login(t, "admin@a.example", adminPW)
	self := "/users/" + jsonNum(float64(adminID))

	if r := e.send(t, http.MethodPatch, self, tok, `{"role":"manager"}`, nil); r.status != http.StatusConflict || r.errCode(t) != "self_lockout" {
		t.Fatalf("self-demote = %d %s, want 409 self_lockout", r.status, r.body)
	}
	if r := e.send(t, http.MethodPatch, self, tok, `{"disabled":true}`, nil); r.status != http.StatusConflict || r.errCode(t) != "self_lockout" {
		t.Fatalf("self-disable = %d %s, want 409 self_lockout", r.status, r.body)
	}
	if r := e.send(t, http.MethodPatch, self, tok, `{"role":"admin"}`, nil); r.status != http.StatusOK {
		t.Fatalf("no-op self role = %d, want 200", r.status)
	}
}

func jsonNum(f float64) string {
	b, _ := json.Marshal(int64(f))
	return string(b)
}
