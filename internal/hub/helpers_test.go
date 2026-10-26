package hub

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"fleetops/internal/auth"
)

// testSecret signs test tokens. It is a fixture, not a deployment secret.
var testSecret = []byte(strings.Repeat("t", 32))

// rig is one hub behind an httptest server, with its own issuer.
type rig struct {
	t      *testing.T
	hub    *Hub
	issuer *auth.Issuer
	url    string
}

// newRig starts a hub with opts. Zero options take the production defaults.
func newRig(t *testing.T, opts Options) *rig {
	t.Helper()
	issuer, err := auth.NewIssuer(testSecret, time.Now)
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	opts.Verifier = issuer

	base, cancel := context.WithCancel(context.Background())
	h := New(base, opts)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Cleanup(cancel)
	return &rig{t: t, hub: h, issuer: issuer, url: "ws" + strings.TrimPrefix(srv.URL, "http") + "/stream"}
}

// token issues a token for user with the given fleet and role.
func (r *rig) token(user, fleet int64, role string) string {
	r.t.Helper()
	return tokenFrom(r.t, r.issuer, user, fleet, role)
}

func tokenFrom(t *testing.T, issuer *auth.Issuer, user, fleet int64, role string) string {
	t.Helper()
	tok, err := issuer.Issue(user, fleet, role)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return tok
}

// newIssuer returns an issuer whose clock is shifted by offset from now.
func newIssuer(t *testing.T, secret []byte, offset time.Duration) *auth.Issuer {
	t.Helper()
	iss, err := auth.NewIssuer(secret, func() time.Time { return time.Now().Add(offset) })
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	return iss
}

// dial opens a client to fleet's room.
func (r *rig) dial(fleet int64) (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, r.url+"?fleet="+itoa(fleet), nil)
	return c, err
}

// dialAuth opens a client and sends the auth frame with tok.
func (r *rig) dialAuth(fleet int64, tok string) *websocket.Conn {
	r.t.Helper()
	c, err := r.dial(fleet)
	if err != nil {
		r.t.Fatalf("dial: %v", err)
	}
	r.t.Cleanup(func() { _ = c.CloseNow() })
	sendAuth(r.t, c, tok)
	return c
}

// sendAuth writes the first-message auth frame.
func sendAuth(t *testing.T, c *websocket.Conn, tok string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"type": "auth", "token": tok})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatalf("write auth: %v", err)
	}
}

// readMsg reads one frame within 10 s.
func readMsg(t *testing.T, c *websocket.Conn) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return data
}

// readAuthOK reads the auth.ok control frame and checks its fields.
func readAuthOK(t *testing.T, c *websocket.Conn, fleet int64, role string) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(readMsg(t, c), &got); err != nil {
		t.Fatalf("decode auth.ok: %v", err)
	}
	if got["type"] != "auth.ok" || got["fleet_id"] != float64(fleet) || got["role"] != role {
		t.Fatalf("auth.ok = %v, want fleet %d role %s", got, fleet, role)
	}
}

// closeCode reads until the connection ends and returns the close status.
func closeCode(t *testing.T, c *websocket.Conn, within time.Duration) websocket.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	for {
		_, _, err := c.Read(ctx)
		if err == nil {
			continue
		}
		return websocket.CloseStatus(err)
	}
}

// waitFor polls cond until it holds or 5 s pass.
func waitFor(t *testing.T, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not reached within 5s", msg)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
