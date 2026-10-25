package auth_test

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

const password = "correct horse battery"

// clock is the one injected clock shared by JWT, throttle, and refresh.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

type authEnv struct {
	url   string
	clock *clock
	db    *sql.DB
	store *users.Store
	iss   *auth.Issuer
}

// startAuth serves the auth routes and one protected route over real HTTP.
func startAuth(t *testing.T) *authEnv {
	t.Helper()
	db := testinfra.NewMigratedDB(t)
	clk := &clock{t: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	iss, err := auth.NewIssuer([]byte(strings.Repeat("s", 32)), clk.now)
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	store := users.NewStore(db)
	h := &auth.Handlers{
		DB:       db,
		Issuer:   iss,
		Throttle: auth.NewThrottle(clk.now),
		Accounts: store,
		Clock:    clk.now,
	}

	e := echo.New()
	e.HideBanner = true
	e.POST("/auth/login", h.Login)
	e.POST("/auth/refresh", h.Refresh)
	e.POST("/auth/logout", h.Logout)
	e.GET("/protected", func(c echo.Context) error {
		p, ok := auth.PrincipalFrom(c)
		if !ok {
			return c.NoContent(http.StatusInternalServerError)
		}
		return c.JSON(http.StatusOK, map[string]any{"user_id": p.UserID, "fleet_id": p.FleetID, "role": p.Role})
	}, auth.RequireAuth(iss))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = e.Server.Serve(ln) }()
	t.Cleanup(func() { _ = e.Close() })

	return &authEnv{url: "http://" + ln.Addr().String(), clock: clk, db: db, store: store, iss: iss}
}

// addUser creates a fleet and one user with the test password.
func (env *authEnv) addUser(t *testing.T, email, role string) users.User {
	t.Helper()
	var fleetID int64
	if err := env.db.QueryRow(`INSERT INTO fleets (name) VALUES ('F') RETURNING id`).Scan(&fleetID); err != nil {
		t.Fatalf("insert fleet: %v", err)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	tx, err := env.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	u, err := env.store.Create(context.Background(), tx, fleetID, email, hash, role)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("create user: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return u
}

type response struct {
	status  int
	body    []byte
	header  http.Header
	cookies []*http.Cookie
}

func (r response) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("decode %q: %v", r.body, err)
	}
	return m
}

// refreshCookie returns the fleetops_refresh cookie from a response, or nil.
func (r response) refreshCookie() *http.Cookie {
	for _, c := range r.cookies {
		if c.Name == auth.RefreshCookieName {
			return c
		}
	}
	return nil
}

// do sends one request over real HTTP. The client has no cookie jar: Secure
// cookies are not sent over plain HTTP, so tests replay them by hand (GF-29).
func (env *authEnv) do(t *testing.T, method, path, bearer string, cookie *http.Cookie, body string) response {
	t.Helper()
	req, err := http.NewRequest(method, env.url+path, strings.NewReader(body))
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
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, body: b, header: resp.Header, cookies: resp.Cookies()}
}

func login(t *testing.T, env *authEnv, email, pw string) response {
	t.Helper()
	return env.do(t, http.MethodPost, "/auth/login", "", nil,
		`{"email":"`+email+`","password":"`+pw+`"}`)
}

func TestLoginProtectedRefreshOldRejected(t *testing.T) {
	env := startAuth(t)
	env.addUser(t, "admin@example.com", "admin")

	resp := login(t, env, "admin@example.com", password)
	if resp.status != http.StatusOK {
		t.Fatalf("login status = %d, body %s", resp.status, resp.body)
	}
	body := resp.json(t)
	access, _ := body["access_token"].(string)
	if access == "" || body["token_type"] != "Bearer" || body["expires_in"] != float64(900) {
		t.Fatalf("login body = %v", body)
	}
	old := resp.refreshCookie()
	if old == nil {
		t.Fatal("login set no refresh cookie")
	}
	if !old.HttpOnly || !old.Secure || old.SameSite != http.SameSiteStrictMode || old.Path != "/auth" || old.MaxAge != 604800 {
		t.Fatalf("refresh cookie attributes wrong: %+v", old)
	}

	if p := env.do(t, http.MethodGet, "/protected", access, nil, ""); p.status != http.StatusOK {
		t.Fatalf("protected status = %d, body %s", p.status, p.body)
	}

	fresh := env.do(t, http.MethodPost, "/auth/refresh", "", old, "")
	if fresh.status != http.StatusOK {
		t.Fatalf("refresh status = %d, body %s", fresh.status, fresh.body)
	}
	if fresh.refreshCookie() == nil || fresh.refreshCookie().Value == old.Value {
		t.Fatal("refresh did not rotate the cookie")
	}

	replay := env.do(t, http.MethodPost, "/auth/refresh", "", old, "")
	if replay.status != http.StatusUnauthorized {
		t.Fatalf("old cookie replay status = %d, want 401", replay.status)
	}
	if got := replay.json(t)["error"]; got != "unauthorized" {
		t.Fatalf("replay error = %v, want unauthorized", got)
	}
}

func TestRefreshWithoutCookieIs401(t *testing.T) {
	env := startAuth(t)
	resp := env.do(t, http.MethodPost, "/auth/refresh", "", nil, "")
	if resp.status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.status)
	}
}

func TestProtectedRouteRejectsMissingAndBadToken(t *testing.T) {
	env := startAuth(t)
	for name, bearer := range map[string]string{"missing": "", "garbage": "abc.def.ghi"} {
		resp := env.do(t, http.MethodGet, "/protected", bearer, nil, "")
		if resp.status != http.StatusUnauthorized {
			t.Errorf("%s token: status = %d, want 401", name, resp.status)
		}
	}
}

func TestDisabledUserCannotLoginOrRefresh(t *testing.T) {
	env := startAuth(t)
	env.addUser(t, "admin@example.com", "admin")
	mgr := env.addUser(t, "manager@example.com", "manager")

	resp := login(t, env, "manager@example.com", password)
	if resp.status != http.StatusOK {
		t.Fatalf("pre-disable login = %d", resp.status)
	}
	cookie := resp.refreshCookie()

	if _, err := env.db.Exec(`UPDATE users SET disabled_at = now() WHERE id = $1`, mgr.ID); err != nil {
		t.Fatalf("disable: %v", err)
	}

	if r := env.do(t, http.MethodPost, "/auth/refresh", "", cookie, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("disabled refresh = %d, want 401", r.status)
	}
	r := login(t, env, "manager@example.com", password)
	if r.status != http.StatusUnauthorized {
		t.Fatalf("disabled login = %d, want 401", r.status)
	}
	if got := r.json(t)["error"]; got != "invalid_credentials" {
		t.Fatalf("disabled login error = %v, want invalid_credentials", got)
	}
}

func TestLoginValidationAndUnknownEmail(t *testing.T) {
	env := startAuth(t)
	if r := env.do(t, http.MethodPost, "/auth/login", "", nil, `{"email":`); r.status != http.StatusBadRequest {
		t.Fatalf("malformed body = %d, want 400", r.status)
	}
	if r := env.do(t, http.MethodPost, "/auth/login", "", nil, `{"email":"","password":"x"}`); r.status != http.StatusBadRequest {
		t.Fatalf("empty email = %d, want 400", r.status)
	}
	r := login(t, env, "nobody@example.com", password)
	if r.status != http.StatusUnauthorized || r.json(t)["error"] != "invalid_credentials" {
		t.Fatalf("unknown email = %d %s, want 401 invalid_credentials", r.status, r.body)
	}
}

func TestLoginThrottleOverHTTP(t *testing.T) {
	env := startAuth(t)
	env.addUser(t, "admin@example.com", "admin")

	for i := 0; i < 5; i++ {
		if r := login(t, env, "admin@example.com", "wrong password value"); r.status != http.StatusUnauthorized {
			t.Fatalf("failure %d = %d, want 401", i+1, r.status)
		}
	}
	sixth := login(t, env, "admin@example.com", "wrong password value")
	if sixth.status != http.StatusTooManyRequests {
		t.Fatalf("sixth attempt = %d, want 429", sixth.status)
	}
	if got := sixth.header.Get("Retry-After"); got == "" || got == "0" {
		t.Fatalf("Retry-After = %q, want > 0", got)
	}
	if got := sixth.json(t)["error"]; got != "rate_limited" {
		t.Fatalf("error = %v, want rate_limited", got)
	}

	// A correct password is still refused inside the window.
	if r := login(t, env, "admin@example.com", password); r.status != http.StatusTooManyRequests {
		t.Fatalf("correct password inside window = %d, want 429", r.status)
	}

	env.clock.t = env.clock.t.Add(15 * time.Minute)
	if r := login(t, env, "admin@example.com", password); r.status != http.StatusOK {
		t.Fatalf("after 15 min = %d, want 200", r.status)
	}
}

func TestLogoutRevokesAndIsIdempotent(t *testing.T) {
	env := startAuth(t)
	env.addUser(t, "admin@example.com", "admin")
	resp := login(t, env, "admin@example.com", password)
	cookie := resp.refreshCookie()

	if r := env.do(t, http.MethodPost, "/auth/logout", "", nil, ""); r.status != http.StatusNoContent {
		t.Fatalf("logout without cookie = %d, want 204", r.status)
	}
	if r := env.do(t, http.MethodPost, "/auth/logout", "", cookie, ""); r.status != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204", r.status)
	}
	if r := env.do(t, http.MethodPost, "/auth/refresh", "", cookie, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("refresh after logout = %d, want 401", r.status)
	}
}
