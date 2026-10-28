package cloudproxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"fleetops/internal/auth"
	"fleetops/internal/fanout"
	"fleetops/internal/hub"
	"fleetops/internal/testinfra"
)

// upstream answers every request with its own name, so the test can see where
// the proxy sent it.
func upstream(t *testing.T, name string) *url.URL {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, name+" "+r.URL.Path) //nolint:gosec // test upstream echoes its own name and the path it was sent
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	return u
}

func get(t *testing.T, base, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(base + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// TestProxyRoutesStreamToHubAndRestToAPI (GF-348): /stream goes to the hub,
// /stream/x and every other path go to the api.
func TestProxyRoutesStreamToHubAndRestToAPI(t *testing.T) {
	front := httptest.NewServer(New(upstream(t, "api"), upstream(t, "hub")))
	t.Cleanup(front.Close)

	cases := map[string]string{
		"/stream":       "hub /stream",
		"/stream/x":     "api /stream/x",
		"/vehicles":     "api /vehicles",
		"/healthz":      "api /healthz",
		"/map/vehicles": "api /map/vehicles",
		"/streamer?x=1": "api /streamer",
	}
	for path, want := range cases {
		_, body := get(t, front.URL, path)
		if body != want {
			t.Errorf("GET %s got %q, want %q", path, body, want)
		}
	}
}

// TestProxyDeniesMetrics (GF-348, GF-329): every /metrics path is 404 on the
// public port and never reaches an upstream.
func TestProxyDeniesMetrics(t *testing.T) {
	front := httptest.NewServer(New(upstream(t, "api"), upstream(t, "hub")))
	t.Cleanup(front.Close)
	for _, path := range []string{"/metrics", "/metrics/x"} {
		if code, body := get(t, front.URL, path); code != http.StatusNotFound || strings.Contains(body, "api") || strings.Contains(body, "hub") {
			t.Errorf("GET %s: status %d body %q, want 404 with no upstream output", path, code, body)
		}
	}
}

// TestProxyForwardsWebSocketUpgrade (GF-348, integration, D-37): a real ws-hub
// on embedded NATS sits behind the proxy. A client dials through the proxy and
// receives a position.batch frame.
func TestProxyForwardsWebSocketUpgrade(t *testing.T) {
	conn := testinfra.EmbeddedNATS(t)
	secret := strings.Repeat("p", 32)
	issuer, err := auth.NewIssuer([]byte(secret), time.Now)
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	base, cancelBase := context.WithCancel(context.Background())
	t.Cleanup(cancelBase)
	h := hub.New(base, hub.Options{RoomCap: 2000, Verifier: issuer})
	subs, err := h.Subscribe(base, hub.NewNATSTransport(conn), conn)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { _ = subs.Close() })
	hubSrv := httptest.NewServer(h)
	t.Cleanup(hubSrv.Close)
	hubURL, _ := url.Parse(hubSrv.URL)

	front := httptest.NewServer(New(upstream(t, "api"), hubURL))
	t.Cleanup(front.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(front.URL, "http") + "/stream?fleet=5"
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial through proxy: %v", err)
	}
	defer func() { _ = c.CloseNow() }()
	tok, err := issuer.Issue(9, 5, "dispatcher")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	first := `{"type":"auth","token":"` + tok + `"}`
	if err := c.Write(ctx, websocket.MessageText, []byte(first)); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	if _, _, err := c.Read(ctx); err != nil {
		t.Fatalf("auth.ok: %v", err)
	}

	// Publish a batch on the same embedded NATS the hub relays from.
	fan := fanout.NewNATSFanout(conn)
	pub := []fanout.Point{{FleetID: 5, DeviceID: 1, Lat: 41, Lng: 29, ServerTS: time.Now().UTC()}}
	if err := fan.PublishPositions(ctx, pub); err != nil {
		t.Fatalf("publish: %v", err)
	}
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		if strings.Contains(string(data), `"position.batch"`) {
			return
		}
	}
}
