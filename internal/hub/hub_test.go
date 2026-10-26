package hub

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestHubAuthOK(t *testing.T) {
	r := newRig(t, Options{})
	c := r.dialAuth(101, r.token(7, 101, "dispatcher"))
	readAuthOK(t, c, 101, "dispatcher")
	waitFor(t, "joined", func() bool { return r.hub.Counters().ConnsOpen.Load() == 1 })
}

func TestHubMissingFleetIs400(t *testing.T) {
	r := newRig(t, Options{})
	for _, q := range []string{"", "?fleet=", "?fleet=abc", "?fleet=0", "?fleet=-4"} {
		_, resp, err := websocket.Dial(context.Background(), r.url+q, nil)
		if err == nil {
			t.Fatalf("fleet %q: dial succeeded", q)
		}
		if resp == nil || resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("fleet %q: status %v, want 400", q, resp)
		}
	}
}

func TestHubAuthTimeoutClose4401(t *testing.T) {
	r := newRig(t, Options{AuthWindow: 300 * time.Millisecond})
	c, err := r.dial(1)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if got := closeCode(t, c, 5*time.Second); got != 4401 {
		t.Fatalf("close code %d, want 4401", got)
	}
	waitFor(t, "auth failure counted", func() bool { return r.hub.Counters().AuthFailed.Load() == 1 })
}

func TestHubAuthBadTokenClose4401(t *testing.T) {
	cases := map[string]func(r *rig) (websocket.MessageType, []byte){
		"bad signature": func(r *rig) (websocket.MessageType, []byte) {
			other := newIssuer(t, bytes.Repeat([]byte("x"), 32), 0)
			tok := tokenFrom(t, other, 7, 1, "driver")
			return websocket.MessageText, authFrame(tok)
		},
		"expired": func(r *rig) (websocket.MessageType, []byte) {
			old := newIssuer(t, testSecret, -time.Hour)
			tok := tokenFrom(t, old, 7, 1, "driver")
			return websocket.MessageText, authFrame(tok)
		},
		"binary frame": func(r *rig) (websocket.MessageType, []byte) {
			return websocket.MessageBinary, authFrame(r.token(7, 1, "driver"))
		},
		"wrong frame type": func(r *rig) (websocket.MessageType, []byte) {
			return websocket.MessageText, []byte(`{"type":"ping"}`)
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, Options{})
			c, err := r.dial(1)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			typ, body := build(r)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.Write(ctx, typ, body); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := closeCode(t, c, 5*time.Second); got != 4401 {
				t.Fatalf("close code %d, want 4401", got)
			}
		})
	}
}

func TestHubFleetMismatchClose4403(t *testing.T) {
	r := newRig(t, Options{})
	c, err := r.dial(1)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	sendAuth(t, c, r.token(7, 2, "admin"))
	if got := closeCode(t, c, 5*time.Second); got != 4403 {
		t.Fatalf("close code %d, want 4403", got)
	}
}

func TestHubClosesAtTokenExpiry(t *testing.T) {
	r := newRig(t, Options{})
	// Issued with an exp two seconds from now.
	short := newIssuer(t, testSecret, -15*time.Minute+2*time.Second)
	c := r.dialAuth(1, tokenFrom(t, short, 7, 1, "admin"))
	readAuthOK(t, c, 1, "admin")

	start := time.Now()
	if got := closeCode(t, c, 5*time.Second); got != 4401 {
		t.Fatalf("close code %d, want 4401", got)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("closed after %s, want within 3s of expiry", d)
	}
}

func TestHubRefusesConnectionAtRoomCap(t *testing.T) {
	r := newRig(t, Options{RoomCap: 2})
	first := r.dialAuth(1, r.token(1, 1, "driver"))
	readAuthOK(t, first, 1, "driver")
	r.dialAuth(1, r.token(2, 1, "driver"))
	waitFor(t, "two joined", func() bool { return r.hub.Counters().ConnsOpen.Load() == 2 })

	_, resp, err := websocket.Dial(context.Background(), r.url+"?fleet=1", nil)
	if err == nil {
		t.Fatal("third dial at cap succeeded")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("third dial status %v, want 503", resp)
	}
	if got := r.hub.Counters().RefusedCap.Load(); got != 1 {
		t.Fatalf("RefusedCap = %d, want 1", got)
	}

	// Closing one client frees its slot for the next dial.
	if err := first.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Logf("client close: %v", err)
	}
	waitFor(t, "slot freed", func() bool {
		c, err := r.dial(1)
		if err != nil {
			return false
		}
		_ = c.CloseNow()
		return true
	})
}

func TestHubEvictsSilentClientOnPongTimeout(t *testing.T) {
	r := newRig(t, Options{PingInterval: 100 * time.Millisecond, PongEvictAfter: 300 * time.Millisecond})
	// The client authenticates and never reads, so it never answers a ping.
	r.dialAuth(1, r.token(1, 1, "driver"))
	waitFor(t, "pong eviction", func() bool { return r.hub.Counters().EvictedPong.Load() == 1 })
}

func TestHubEvictsSlowConsumer(t *testing.T) {
	r := newRig(t, Options{})
	c := r.dialAuth(1, r.token(1, 1, "admin"))
	readAuthOK(t, c, 1, "admin")
	waitFor(t, "joined", func() bool { return r.hub.Counters().ConnsOpen.Load() == 1 })

	// Flood until the bounded queue and the socket buffer are full.
	frame := bytes.Repeat([]byte("x"), 16<<10)
	for i := 0; i < 20000 && r.hub.Counters().EvictedSlow.Load() == 0; i++ {
		r.hub.broadcast(1, frame, nil)
	}
	waitFor(t, "slow eviction", func() bool { return r.hub.Counters().EvictedSlow.Load() == 1 })
	if got := closeCode(t, c, 15*time.Second); got != 4408 {
		t.Fatalf("close code %d, want 4408", got)
	}
}

func TestHubRoomIsolation(t *testing.T) {
	r := newRig(t, Options{})
	a := r.dialAuth(1, r.token(1, 1, "admin"))
	readAuthOK(t, a, 1, "admin")
	b := r.dialAuth(2, r.token(2, 2, "admin"))
	readAuthOK(t, b, 2, "admin")
	waitFor(t, "both joined", func() bool { return r.hub.Counters().ConnsOpen.Load() == 2 })

	r.hub.broadcast(1, []byte(`{"room":"a"}`), nil)
	if got := string(readMsg(t, a)); got != `{"room":"a"}` {
		t.Fatalf("fleet 1 got %s", got)
	}
	r.hub.broadcast(2, []byte(`{"room":"b"}`), nil)
	// Fleet 2's first frame must be its own: nothing from fleet 1 leaked in.
	if got := string(readMsg(t, b)); got != `{"room":"b"}` {
		t.Fatalf("fleet 2 got %s, want its own frame first", got)
	}
}

// authFrame builds the first-message auth body for tok.
func authFrame(tok string) []byte {
	return []byte(`{"type":"auth","token":"` + tok + `"}`)
}
