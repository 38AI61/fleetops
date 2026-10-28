// Command ingest drains the GPS_RAW stream into positions and runs partition
// maintenance (P6, GF-94). Its only listener serves /metrics (P22, GF-330).
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	"fleetops/internal/alerts"
	"fleetops/internal/config"
	"fleetops/internal/eta"
	"fleetops/internal/fanout"
	"fleetops/internal/ingest"
	"fleetops/internal/observability"
	"fleetops/internal/presence"
	"fleetops/internal/reports"
)

// metricsAddr is the /metrics listener. It is the reserved ingest metrics port (PORTS.md, GF-330).
var metricsAddr = flag.String("metrics-addr", "127.0.0.1:10008", "metrics listen address")

func main() {
	flag.Parse()
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
	// nats mode skips Redis and keeps presence in memory (GF-347).
	var rdb *redis.Client
	var store presence.Store = presence.NewMemoryStore(time.Now)
	if cfg.FanoutMode == config.FanoutRedis {
		ropts, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			return err
		}
		rdb = redis.NewClient(ropts)
		defer func() { _ = rdb.Close() }()
		pingCtx, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
		err = rdb.Ping(pingCtx).Err()
		cancelPing()
		if err != nil {
			return err
		}
		store = presence.NewRedisStore(rdb)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := ingest.EnsureStream(ctx, js); err != nil {
		return err
	}

	// One registry carries ingest, eta, reports and alerts series (GF-329).
	reg := observability.NewRegistry()
	metricsLn, err := net.Listen("tcp", *metricsAddr)
	if err != nil {
		return err
	}
	metricsSrv := &http.Server{Handler: metricsMux(reg), ReadHeaderTimeout: 10 * time.Second}
	metricsDone := make(chan struct{})
	go func() {
		defer close(metricsDone)
		if err := metricsSrv.Serve(metricsLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("ingest: metrics: %v", err)
		}
	}()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = metricsSrv.Shutdown(shutCtx)
		<-metricsDone
	}()
	log.Printf("ingest: metrics on %s", metricsLn.Addr())

	metrics := ingest.NewMetrics(reg)
	counters := &ingest.Counters{}
	buf := ingest.NewBuffer(cfg.FlushInterval, cfg.FlushMax, cfg.AccuracyMaxM, counters)
	writer := ingest.NewWriter(db, counters)

	fan, err := fanout.New(cfg.FanoutMode, rdb, nc)
	if err != nil {
		return err
	}
	tracker := presence.NewTracker(store, fan, cfg.PresenceTTL)
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
	etaLoop := &eta.Loop{DB: db, Router: eta.NewOSRMRouter(cfg.OSRMURL), Limiter: eta.NewLimiter(eta.AttemptWindow), Clock: time.Now, Metrics: eta.NewMetrics(reg), Publisher: nc}
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

	// The daily rollup stops and is joined before the DB closes, beside the ETA loop (GF-320).
	rollup := &reports.Loop{DB: db, Clock: time.Now, Metrics: reports.NewMetrics(reg)}
	reportsCtx, cancelReports := context.WithCancel(ctx)
	reportsDone := make(chan struct{})
	go func() {
		defer close(reportsDone)
		rollup.Run(reportsCtx, reports.Interval)
	}()
	defer func() {
		cancelReports()
		<-reportsDone
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
