// Command ingest drains the GPS_RAW stream into positions and runs partition
// maintenance (P6, GF-94). It has no HTTP listener: /metrics is P22.
package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"fleetops/internal/alerts"
	"fleetops/internal/config"
	"fleetops/internal/eta"
	"fleetops/internal/fanout"
	"fleetops/internal/ingest"
	"fleetops/internal/presence"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("ingest: %v", err)
	}
}

// run holds the defers so every resource closes in the GF-95 order.
func run() error {
	// config.Load refuses a short JWT_SECRET or DEVICE_KEY_SALT, as the API does.
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if err := db.Ping(); err != nil {
		return err
	}

	nc, err := nats.Connect(cfg.NATSURL)
	if err != nil {
		return err
	}
	defer nc.Close()
	js, err := ingest.NewJetStream(nc)
	if err != nil {
		return err
	}

	// Redis is pinged at boot so a bad REDIS_URL fails fast (GF-103 covers runtime).
	ropts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return err
	}
	rdb := redis.NewClient(ropts)
	defer func() { _ = rdb.Close() }()
	pingCtx, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
	err = rdb.Ping(pingCtx).Err()
	cancelPing()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := ingest.EnsureStream(ctx, js); err != nil {
		return err
	}

	// The registry is private to this process until P22 exposes it.
	reg := prometheus.NewRegistry()
	metrics := ingest.NewMetrics(reg)
	counters := &ingest.Counters{}
	buf := ingest.NewBuffer(cfg.FlushInterval, cfg.FlushMax, cfg.AccuracyMaxM, counters)
	writer := ingest.NewWriter(db, counters)

	fan := fanout.NewRedisFanout(rdb)
	tracker := presence.NewTracker(rdb, fan, cfg.PresenceTTL)
	pub := &ingest.Publisher{Fanout: fan, Tracker: tracker, Metrics: metrics}

	// The sweeper stops and is joined before Redis closes (GF-118).
	sweeper := presence.NewSweeper(db, tracker, pub.RecordSweep)
	sweepCtx, cancelSweep := context.WithCancel(ctx)
	sweepDone := make(chan struct{})
	go func() {
		defer close(sweepDone)
		sweeper.Run(sweepCtx)
	}()
	defer func() {
		cancelSweep()
		<-sweepDone
	}()

	// The alert evaluator stops and is joined before the DB closes (GF-291).
	evaluator := &alerts.Evaluator{DB: db, Clock: time.Now, Metrics: alerts.NewMetrics(reg), Publisher: nc, Notifier: alerts.LogNotifier{}}
	evalCtx, cancelEval := context.WithCancel(ctx)
	evalDone := make(chan struct{})
	go func() {
		defer close(evalDone)
		evaluator.Run(evalCtx, alerts.Interval)
	}()
	defer func() {
		cancelEval()
		<-evalDone
	}()

	// The ETA loop stops and is joined before the DB closes, beside the evaluator (GF-306).
	etaLoop := &eta.Loop{DB: db, Router: eta.NewOSRMRouter(cfg.OSRMURL), Limiter: eta.NewLimiter(eta.AttemptWindow), Clock: time.Now, Metrics: eta.NewMetrics(reg)}
	etaCtx, cancelETA := context.WithCancel(ctx)
	etaDone := make(chan struct{})
	go func() {
		defer close(etaDone)
		etaLoop.Run(etaCtx)
	}()
	defer func() {
		cancelETA()
		<-etaDone
	}()

	// Boot order: maintenance first pass (ensure, then retain), then the consumer.
	maint := &ingest.Maintenance{DB: db, Clock: time.Now, Metrics: metrics}
	maintCtx, cancelMaint := context.WithCancel(ctx)
	defer cancelMaint()
	booted := make(chan struct{})
	maintDone := make(chan struct{})
	go func() {
		defer close(maintDone)
		maint.Run(maintCtx, func() { close(booted) })
	}()
	<-booted

	if err := ingest.EnsureConsumer(ctx, js); err != nil {
		return err
	}

	log.Printf("ingest: consuming %s with flush interval %s, flush max %d", ingest.StreamName, cfg.FlushInterval, cfg.FlushMax)
	consumer := ingest.NewConsumer(js, buf, writer, metrics, time.Now, pub)
	// Run returns after the buffer drains. Its error is a startup failure only.
	err = consumer.Run(ctx)
	cancelMaint()
	<-maintDone
	return err
}
