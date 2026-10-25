// Package hub is the WebSocket fan-out for one fleet room per fleet_id (D-21).
// Clients authenticate with a first message, then receive relayed events until
// they leave, expire, fall behind, or the process shuts down (GF-110 to GF-118).
package hub

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"fleetops/internal/auth"
)

// Defaults for the timers and cap (GF-112, GF-120). Services use these values;
// tests shorten the timers through Options.
const (
	DefaultRoomCap        = 2000
	DefaultAuthWindow     = 5 * time.Second
	DefaultPingInterval   = 20 * time.Second
	DefaultPongEvictAfter = 60 * time.Second
)

// Internal constants (GF-113, GF-120).
const (
	sendQueue     = 256
	writeDeadline = 5 * time.Second
	authReadLimit = 4096
)

// defaultOriginPatterns are the reserved vite dev ports (GF-110).
var defaultOriginPatterns = []string{
	"localhost:10012", "127.0.0.1:10012",
	"localhost:10013", "127.0.0.1:10013",
}

// Options configures a Hub. Zero durations and a zero RoomCap take the defaults.
// OriginPatterns nil takes the vite dev origins.
type Options struct {
	RoomCap        int
	AuthWindow     time.Duration
	PingInterval   time.Duration
	PongEvictAfter time.Duration
	Verifier       *auth.Issuer
	OriginPatterns []string
}

// Counters are read by tests only; no metrics endpoint exists before P22 (GF-121).
type Counters struct {
	ConnsOpen       atomic.Int64
	RefusedCap      atomic.Int64
	AuthFailed      atomic.Int64
	EvictedPong     atomic.Int64
	EvictedSlow     atomic.Int64
	RelayedFrames   atomic.Int64
	DroppedEnvelope atomic.Int64
}

// Hub serves GET /stream. It is an http.Handler; mount it at /stream.
type Hub struct {
	base     context.Context
	opts     Options
	counters Counters

	mu    sync.Mutex
	rooms map[int64]*room
}

// New returns a hub. Its connections end with base: when base is cancelled,
// each one closes with 1001 (GF-118). Options gaps take the defaults.
func New(base context.Context, opts Options) *Hub {
	if opts.RoomCap == 0 {
		opts.RoomCap = DefaultRoomCap
	}
	if opts.AuthWindow == 0 {
		opts.AuthWindow = DefaultAuthWindow
	}
	if opts.PingInterval == 0 {
		opts.PingInterval = DefaultPingInterval
	}
	if opts.PongEvictAfter == 0 {
		opts.PongEvictAfter = DefaultPongEvictAfter
	}
	if opts.OriginPatterns == nil {
		opts.OriginPatterns = defaultOriginPatterns
	}
	return &Hub{base: base, opts: opts, rooms: map[int64]*room{}}
}

// Counters returns the hub's counters. They are read by tests only (GF-121).
func (h *Hub) Counters() *Counters {
	return &h.counters
}

// ServeHTTP runs the pre-upgrade checks, the upgrade, and the connection (GF-110).
// Refusals before the upgrade are plain HTTP, because a 503 after 101 is impossible.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fleet, err := strconv.ParseInt(r.URL.Query().Get("fleet"), 10, 64)
	if err != nil || fleet <= 0 {
		http.Error(w, "fleet must be a positive integer", http.StatusBadRequest)
		return
	}
	if !h.reserve(fleet) {
		h.counters.RefusedCap.Add(1)
		http.Error(w, "room full", http.StatusServiceUnavailable)
		return
	}
	defer h.release(fleet)

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: h.opts.OriginPatterns})
	if err != nil {
		// Accept has already written the refusal.
		return
	}
	h.serve(ws, fleet)
}
