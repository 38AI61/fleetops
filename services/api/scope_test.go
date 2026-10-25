package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"fleetops/internal/auth"
)

// TestFleetScoping is the fleet-isolation check: a fleet A admin cannot see or
// change fleet B users, and a token with a forged fleet_id is refused.
func TestFleetScoping(t *testing.T) {
	h := startServer(t)
	fleetA := h.fleet(t, "A")
	fleetB := h.fleet(t, "B")
	adminA := h.user(t, fleetA, "admin@a.example", "admin")
	userB := h.user(t, fleetB, "user@b.example", "dispatcher")
	tokA := h.login(t, "admin@a.example")
	userBPath := fmt.Sprintf("/users/%d", userB)

	t.Run("GET /users excludes fleet B", func(t *testing.T) {
		r := h.do(t, http.MethodGet, "/users", tokA, "", nil)
		if r.status != http.StatusOK {
			t.Fatalf("status = %d, want 200", r.status)
		}
		if strings.Contains(string(r.body), "user@b.example") {
			t.Fatalf("fleet A listing leaks fleet B user: %s", r.body)
		}
		if !strings.Contains(string(r.body), "admin@a.example") {
			t.Fatalf("fleet A listing missing its own admin: %s", r.body)
		}
	})

	t.Run("PATCH fleet B user is 404 and writes nothing", func(t *testing.T) {
		var before int
		if err := h.db.QueryRow(`SELECT count(*) FROM admin_audit`).Scan(&before); err != nil {
			t.Fatalf("count audit: %v", err)
		}
		r := h.do(t, http.MethodPatch, userBPath, tokA, `{"role":"driver"}`, nil)
		if r.status != http.StatusNotFound || r.errCode(t) != "not_found" {
			t.Fatalf("status = %d %s, want 404 not_found", r.status, r.body)
		}
		var role string
		if err := h.db.QueryRow(`SELECT role FROM users WHERE id = $1`, userB).Scan(&role); err != nil {
			t.Fatalf("read role: %v", err)
		}
		if role != "dispatcher" {
			t.Fatalf("fleet B role changed to %q through fleet A", role)
		}
		var after int
		if err := h.db.QueryRow(`SELECT count(*) FROM admin_audit`).Scan(&after); err != nil {
			t.Fatalf("count audit: %v", err)
		}
		if after != before {
			t.Fatalf("cross-fleet attempt wrote %d audit rows", after-before)
		}
	})

	t.Run("token signed with another key is 401", func(t *testing.T) {
		other, err := auth.NewIssuer([]byte(strings.Repeat("x", 32)), h.clock.now)
		if err != nil {
			t.Fatalf("NewIssuer: %v", err)
		}
		forged, err := other.Issue(adminA, fleetB, "admin")
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		r := h.do(t, http.MethodPatch, userBPath, forged, `{"role":"driver"}`, nil)
		if r.status != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", r.status)
		}
	})

	t.Run("edited fleet_id on a real token is 401", func(t *testing.T) {
		forged := retargetFleet(t, tokA, fleetB)
		r := h.do(t, http.MethodPatch, userBPath, forged, `{"role":"driver"}`, nil)
		if r.status != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", r.status)
		}
		if r := h.do(t, http.MethodGet, "/users", forged, "", nil); r.status != http.StatusUnauthorized {
			t.Fatalf("GET with forged fleet_id = %d, want 401", r.status)
		}
	})
}

// retargetFleet rewrites fleet_id in a token's payload and keeps the old
// signature, which must then fail verification.
func retargetFleet(t *testing.T, tok string, fleetID int64) string {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	claims["fleet_id"] = fleetID
	edited, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	parts[1] = base64.RawURLEncoding.EncodeToString(edited)
	return strings.Join(parts, ".")
}
