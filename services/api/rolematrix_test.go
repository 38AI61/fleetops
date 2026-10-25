package main

import (
	"fmt"
	"net/http"
	"testing"
)

// roleCols are the callers of the role matrix (R-6). "" means no token.
var roleCols = []struct{ name, role string }{
	{"admin", "admin"},
	{"manager", "manager"},
	{"dispatcher", "dispatcher"},
	{"driver", "driver"},
	{"unauthenticated", ""},
}

// TestRoleMatrix is the R-6 table: every P2 user verb against every caller.
// Expected values follow §10 and GF-28. Future phases add rows to this table.
func TestRoleMatrix(t *testing.T) {
	h := startServer(t)
	fleet := h.fleet(t, "A")
	tokens := map[string]string{"": ""}
	for _, c := range roleCols {
		if c.role == "" {
			continue
		}
		email := c.role + "@a.example"
		h.user(t, fleet, email, c.role)
		tokens[c.role] = h.login(t, email)
	}
	target := h.user(t, fleet, "target@a.example", "dispatcher")
	targetPath := fmt.Sprintf("/users/%d", target)

	const (
		ok200 = http.StatusOK
		ok201 = http.StatusCreated
		forb  = http.StatusForbidden
		unau  = http.StatusUnauthorized
		bad   = http.StatusBadRequest
	)
	// want[row][caller], in roleCols order: admin, manager, dispatcher, driver, unauthenticated.
	rows := []struct {
		name   string
		method string
		path   string
		body   func(n int) string
		want   [5]int
	}{
		{"GET /users", http.MethodGet, "/users", func(int) string { return "" }, [5]int{ok200, forb, forb, forb, unau}},
		{"POST /users", http.MethodPost, "/users", func(n int) string {
			return fmt.Sprintf(`{"email":"new%d@a.example","password":"%s","role":"driver"}`, n, testPassword)
		}, [5]int{ok201, forb, forb, forb, unau}},
		{"PATCH /users/:id", http.MethodPatch, targetPath, func(int) string {
			return `{"role":"dispatcher"}`
		}, [5]int{ok200, forb, forb, forb, unau}},
		// GF-28: role check runs before body parsing, so a malformed body is 403 for non-admins.
		{"POST /users (malformed body)", http.MethodPost, "/users", func(int) string {
			return `{"email":`
		}, [5]int{bad, forb, forb, forb, unau}},
	}

	seq := 0 // gives each POST a unique email
	for _, row := range rows {
		for i, c := range roleCols {
			seq++
			n := seq
			t.Run(row.name+"/"+c.name, func(t *testing.T) {
				r := h.do(t, row.method, row.path, tokens[c.role], row.body(n), nil)
				if r.status != row.want[i] {
					t.Fatalf("status = %d (%s), want %d", r.status, r.body, row.want[i])
				}
			})
		}
	}

	// Login, refresh, and logout take no JWT; their expected results come from §8.
	t.Run("no-JWT/POST /auth/login valid credentials", func(t *testing.T) {
		r := h.do(t, http.MethodPost, "/auth/login", "",
			fmt.Sprintf(`{"email":"admin@a.example","password":"%s"}`, testPassword), nil)
		if r.status != ok200 {
			t.Fatalf("status = %d, want 200", r.status)
		}
	})
	t.Run("no-JWT/POST /auth/login wrong password", func(t *testing.T) {
		r := h.do(t, http.MethodPost, "/auth/login", "",
			`{"email":"admin@a.example","password":"wrong password long"}`, nil)
		if r.status != unau || r.errCode(t) != "invalid_credentials" {
			t.Fatalf("status = %d %s, want 401 invalid_credentials", r.status, r.body)
		}
	})
	t.Run("no-JWT/POST /auth/refresh without cookie", func(t *testing.T) {
		if r := h.do(t, http.MethodPost, "/auth/refresh", "", "", nil); r.status != unau {
			t.Fatalf("status = %d, want 401", r.status)
		}
	})
	t.Run("no-JWT/POST /auth/logout without cookie", func(t *testing.T) {
		if r := h.do(t, http.MethodPost, "/auth/logout", "", "", nil); r.status != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", r.status)
		}
	})
}
