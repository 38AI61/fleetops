package main

import (
	"net/http"
	"strings"
	"testing"
)

// TestHealthzUnauthenticated (GF-354): /healthz answers 200 with no token, and
// the same request on a JWT route is 401.
func TestHealthzUnauthenticated(t *testing.T) {
	h := startServer(t)
	r := h.do(t, http.MethodGet, "/healthz", "", "", nil)
	if r.status != http.StatusOK || strings.TrimSpace(string(r.body)) != `{"status":"ok"}` {
		t.Fatalf("healthz status %d body %q, want 200 ok", r.status, r.body)
	}
	if r := h.do(t, http.MethodGet, "/vehicles", "", "", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("vehicles without token: status %d, want 401", r.status)
	}
}
