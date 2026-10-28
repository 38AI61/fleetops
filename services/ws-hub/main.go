// Command ws-hub serves the fleet WebSocket stream (P7, GF-118). It relays
// Redis position and presence events and NATS job and alert events to each
// fleet's room. It has no database access.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	"fleetops/internal/auth"
	"fleetops/internal/config"
	"fleetops/internal/hub"
	"fleetops/internal/observability"
)

// addr is the listen address. It is the reserved ws-hub host port (PORTS.md).
var listen = flag.String("addr", "127.0.0.1:10011", "listen address")

// Shutdown budgets (GF-118, GF-120).
const (
	httpShutdownTimeout = 5 * time.Second
	connDrainTimeout    = 5 * time.Second
	redisPingTimeout    = 5 * time.Second
)

func main() {
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatalf("ws-hub: %v", err)
	}
}

// run boots the hub and returns after ctx is cancelled and shutdown completes.
// Boot order: config, NATS, Redis (pinged), issuer, Redis PSUBSCRIBE and NATS
// subscribe, then listen (GF-118). Shutdown: stop upgrades, cancel the base
// context so each connection closes 1001, wait up to 5 s, then close the
// subscriptions, NATS, and Redis.
func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	nc, err := nats.Connect(cfg.NATSURL)
	if err != nil {
		return err
	}
	defer nc.Close()

	ropts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return err
	}
	rdb := redis.NewClient(ropts)
	defer func() { _ = rdb.Close() }()
	pingCtx, cancelPing := context.WithTimeout(ctx, redisPingTimeout)
	err = rdb.Ping(pingCtx).Err()
	cancelPing()
	if err != nil {
		return err
	}

	issuer, err := auth.NewIssuer([]byte(cfg.JWTSecret), time.Now)
	if err != nil {
		return err
	}

	// The base context is cancelled only after Shutdown stops new upgrades.
	base, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	h := hub.New(base, hub.Options{RoomCap: cfg.WSRoomCap, Verifier: issuer})

	subs, err := h.Subscribe(ctx, rdb, nc)
	if err != nil {
		return err
	}
	defer func() { _ = subs.Close() }()

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	// /metrics sits beside /stream on the loopback listener (GF-330). It is not behind JWT (GF-329).
	reg := observability.NewRegistry()
	mux := http.NewServeMux()
	mux.Handle("/stream", h)
	mux.Handle("/metrics", observability.Handler(reg))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	log.Printf("ws-hub: listening on %s", ln.Addr())

	select {
	case err := <-serveErr:
		cancelBase()
		return err
	case <-ctx.Done():
	}

	log.Printf("ws-hub: shutting down")
	shutCtx, cancelShut := context.WithTimeout(context.Background(), httpShutdownTimeout)
	defer cancelShut()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("ws-hub: http shutdown: %v", err)
	}
	cancelBase()

	drainCtx, cancelDrain := context.WithTimeout(context.Background(), connDrainTimeout)
	defer cancelDrain()
	if err := h.Wait(drainCtx); err != nil {
		log.Printf("ws-hub: connections still open after %s: %v", connDrainTimeout, err)
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
