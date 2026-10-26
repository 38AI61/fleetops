package hub

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Application close codes (GF-111).
const (
	closeUnauthorized  websocket.StatusCode = 4401
	closeFleetMismatch websocket.StatusCode = 4403
	closeSlowConsumer  websocket.StatusCode = 4408
	closeGoingAway     websocket.StatusCode = 1001
)

// roles that may connect (GF-110 step 7).
var allowedRoles = map[string]bool{"admin": true, "manager": true, "dispatcher": true, "driver": true}

// conn is one authenticated WebSocket. Its goroutines are the reader (CloseRead),
// one writer, one pinger, the expiry timer, and the closer (GF-113 to GF-115).
type conn struct {
	hub     *Hub
	ws      *websocket.Conn
	fleet   int64
	role    string
	subject int64
	expires time.Time

	send     chan []byte
	cancel   context.CancelFunc
	lastPong atomic.Int64 // unix nanoseconds of the last answered ping

	closeMu     sync.Mutex
	closeSet    bool
	closeCode   websocket.StatusCode
	closeReason string
	abrupt      bool
}

// kill records how the connection ends and cancels it. The first call wins and
// reports whether it did, so each eviction counts once (GF-111).
func (c *conn) kill(code websocket.StatusCode, reason string, abrupt bool) bool {
	c.closeMu.Lock()
	if c.closeSet {
		c.closeMu.Unlock()
		return false
	}
	c.closeSet = true
	c.closeCode, c.closeReason, c.abrupt = code, reason, abrupt
	c.closeMu.Unlock()
	c.cancel()
	return true
}

// closeWire sends the one close the connection ends with. It runs once the
// connection context is done. Any Read in progress is unblocked by Close.
// A connection that ended without a recorded reason closes 1001 (GF-118).
func (c *conn) closeWire() {
	c.closeMu.Lock()
	set, code, reason, abrupt := c.closeSet, c.closeCode, c.closeReason, c.abrupt
	c.closeMu.Unlock()

	if abrupt {
		_ = c.ws.CloseNow()
		return
	}
	if !set {
		code, reason = closeGoingAway, "server shutdown"
	}
	_ = c.ws.Close(code, reason)
}

// closing reports whether the connection has been killed.
func (c *conn) closing() bool {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	return c.closeSet
}

// enqueue hands a frame to the writer without blocking. A full queue evicts
// the client as a slow consumer (GF-113).
func (c *conn) enqueue(frame []byte) {
	select {
	case c.send <- frame:
	default:
		if c.kill(closeSlowConsumer, "slow consumer", false) {
			c.hub.counters.EvictedSlow.Add(1)
		}
	}
}

// serve runs one connection after its upgrade. It returns when the connection
// is closed and every goroutine it started has stopped.
func (h *Hub) serve(ws *websocket.Conn, fleet int64) {
	ctx, cancel := context.WithCancel(h.base)
	c := &conn{hub: h, ws: ws, fleet: fleet, send: make(chan []byte, sendQueue), cancel: cancel}

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		<-ctx.Done()
		c.closeWire()
	}()

	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
		<-closed
	}()

	if !c.authenticate() {
		return
	}
	h.join(c)
	defer h.leave(c)

	c.lastPong.Store(time.Now().UnixNano())
	expiry := time.AfterFunc(time.Until(c.expires), func() {
		c.kill(closeUnauthorized, "token expired", false)
	})
	defer expiry.Stop()

	// CloseRead discards client data frames and answers their control frames.
	// Its context outlives ctx, because cancelling it would drop the close frame.
	readDone := c.ws.CloseRead(context.Background())
	go func() {
		<-readDone.Done()
		cancel()
	}()

	wg.Add(2)
	go func() {
		defer wg.Done()
		c.writer(ctx)
	}()
	go func() {
		defer wg.Done()
		c.pinger(ctx)
	}()

	<-ctx.Done()
}

// authOK is the control frame that acknowledges a good first message (GF-117).
type authOK struct {
	Type    string `json:"type"`
	FleetID int64  `json:"fleet_id"`
	Role    string `json:"role"`
}

// authenticate reads the first frame and checks it (GF-110 steps 4 to 8). Every
// refusal records its close, counts it, and returns false. The auth window
// closes the connection with 4401 if no frame arrives (D-21).
func (c *conn) authenticate() bool {
	timer := time.AfterFunc(c.hub.opts.AuthWindow, func() {
		c.rejectAuth(closeUnauthorized, "unauthorized")
	})
	c.ws.SetReadLimit(authReadLimit)
	// Read has no context: a context that ends mid-read drops the socket
	// without a close frame. The closer unblocks this Read instead.
	typ, data, err := c.ws.Read(context.Background())
	timer.Stop()
	if c.closing() {
		return false
	}
	if err != nil || typ != websocket.MessageText {
		return c.rejectAuth(closeUnauthorized, "unauthorized")
	}

	var frame struct {
		Type  string `json:"type"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &frame); err != nil || frame.Type != "auth" {
		return c.rejectAuth(closeUnauthorized, "unauthorized")
	}
	claims, err := c.hub.opts.Verifier.Verify(frame.Token)
	if err != nil || claims.ExpiresAt == nil {
		return c.rejectAuth(closeUnauthorized, "unauthorized")
	}
	if claims.FleetID != c.fleet {
		return c.rejectAuth(closeFleetMismatch, "fleet mismatch")
	}
	if !allowedRoles[claims.Role] {
		return c.rejectAuth(closeUnauthorized, "unauthorized")
	}
	subject, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return c.rejectAuth(closeUnauthorized, "unauthorized")
	}
	c.role, c.subject, c.expires = claims.Role, subject, claims.ExpiresAt.Time

	ack, err := json.Marshal(authOK{Type: "auth.ok", FleetID: c.fleet, Role: c.role})
	if err != nil {
		return c.rejectAuth(websocket.StatusInternalError, "internal error")
	}
	wctx, cancel := context.WithTimeout(context.Background(), writeDeadline)
	err = c.ws.Write(wctx, websocket.MessageText, ack)
	cancel()
	if err != nil {
		c.kill(websocket.StatusInternalError, "write failed", false)
		return false
	}
	return true
}

// rejectAuth ends a connection that failed authentication and counts it once.
func (c *conn) rejectAuth(code websocket.StatusCode, reason string) bool {
	if c.kill(code, reason, false) {
		c.hub.counters.AuthFailed.Add(1)
	}
	return false
}

// writer drains the send queue, one frame at a time, with a per-write deadline (GF-113).
func (c *conn) writer(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-c.send:
			wctx, cancel := context.WithTimeout(context.Background(), writeDeadline)
			err := c.ws.Write(wctx, websocket.MessageText, frame)
			cancel()
			if err != nil {
				c.kill(websocket.StatusInternalError, "write failed", false)
				return
			}
			c.hub.counters.RelayedFrames.Add(1)
		}
	}
}

// pinger pings every PingInterval and evicts a connection whose last pong is
// PongEvictAfter old. Eviction is abrupt: an unresponsive peer gets no close
// handshake (GF-114).
func (c *conn) pinger(ctx context.Context) {
	ticker := time.NewTicker(c.hub.opts.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		last := time.Unix(0, c.lastPong.Load())
		if time.Since(last) >= c.hub.opts.PongEvictAfter {
			if c.kill(0, "pong timeout", true) {
				c.hub.counters.EvictedPong.Add(1)
			}
			return
		}
		// Background, not ctx: a cancelled write would drop the socket, not close it.
		pctx, cancel := context.WithTimeout(context.Background(), c.hub.opts.PingInterval)
		if c.ws.Ping(pctx) == nil {
			c.lastPong.Store(time.Now().UnixNano())
		}
		cancel()
	}
}
