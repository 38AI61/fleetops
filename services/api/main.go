package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"

	"fleetops/internal/auth"
	"fleetops/internal/config"
	"fleetops/internal/ingest"
	"fleetops/internal/observability"
	"fleetops/internal/proofs"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:10010", "listen address (10010 is the reserved local api port)")
	flag.Parse()

	// config.Load refuses a short JWT_SECRET or DEVICE_KEY_SALT before the database is opened.
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("api: %v", err)
	}
	issuer, err := auth.NewIssuer([]byte(cfg.JWTSecret), time.Now)
	if err != nil {
		log.Fatalf("api: %v", err)
	}

	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("api: open database: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Ping(); err != nil {
		log.Fatalf("api: ping database: %v", err)
	}

	// NATS refuses startup on failure: a gateway that cannot publish must not serve (P4).
	nc, err := nats.Connect(cfg.NATSURL)
	if err != nil {
		log.Fatalf("api: connect nats: %v", err)
	}
	defer nc.Close()
	js, err := ingest.NewJetStream(nc)
	if err != nil {
		log.Fatalf("api: jetstream: %v", err)
	}
	if err := ingest.EnsureStream(context.Background(), js); err != nil {
		log.Fatalf("api: ensure %s stream: %v", ingest.StreamName, err)
	}

	// Proof photos (D-07, GF-201). Boot refuses to start when MinIO is unreachable or the bucket cannot be ensured.
	proofStore, err := proofs.FromEnv(os.Getenv)
	if err != nil {
		log.Fatalf("api: proofs: %v", err)
	}
	if err := proofStore.EnsureBucket(context.Background()); err != nil {
		log.Fatalf("api: ensure %s bucket: %v", proofs.Bucket, err)
	}

	// OTLP tracing is a no-op unless OTEL_EXPORTER_OTLP_ENDPOINT is set (D-36, GF-334).
	shutdownTracing, err := observability.InitTracing(context.Background(), os.LookupEnv, "fleetops-api")
	if err != nil {
		log.Fatalf("api: tracing: %v", err)
	}
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracing(shutCtx)
	}()

	e := NewServer(Deps{
		DB:            db,
		Issuer:        issuer,
		Clock:         time.Now,
		Throttle:      auth.NewThrottle(time.Now),
		DeviceKeySalt: []byte(cfg.DeviceKeySalt),
		NATS:          js,
		Gate:          ingest.NewGate(),
		PresenceTTL:   cfg.PresenceTTL,
		Events:        nc,
		Proofs:        proofStore,
		Tracer:        otel.Tracer("fleetops/jobs"),
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = e.Shutdown(shutdownCtx)
	}()

	log.Printf("api: listening on %s", *addr)
	if err := e.Start(*addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("api: %v", err)
	}
}
