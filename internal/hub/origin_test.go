package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"fleetops/internal/auth"
)

// TestHubAcceptsConfiguredOrigin (GF-353): an Origin that matches a configured
// pattern upgrades, and a default vite origin is refused once patterns are set.
func TestHubAcceptsConfiguredOrigin(t *testing.T) {
	issuer, err := auth.NewIssuer([]byte(strings.Repeat("o", 32)), time.Now)
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	base, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := New(base, Options{RoomCap: 2000, Verifier: issuer, OriginPatterns: []string{"fleetops.pages.dev"}})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/stream?fleet=1"

	dial := func(origin string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
			HTTPHeader: http.Header{"Origin": []string{origin}},
		})
		if c != nil {
			_ = c.CloseNow()
		}
		return err
	}

	if err := dial("https://fleetops.pages.dev"); err != nil {
		t.Fatalf("configured origin refused: %v", err)
	}
	if err := dial("http://localhost:10012"); err == nil {
		t.Fatal("default vite origin accepted although WS origin patterns are set")
	}
}
