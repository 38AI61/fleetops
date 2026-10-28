// Command fleetops-cloud is the cloud container's supervisor (P24, GF-348,
// GF-349, GF-350). It runs the embedded NATS server and the single-port front
// proxy, starts MinIO, and supervises fleetops-api, fleetops-ingest and
// fleetops-ws-hub as separate processes. Logs go to stdout and stderr only.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"fleetops/internal/cloudproxy"
)

// Fixed loopback layout inside the container (GF-349, GF-350). The api and
// ws-hub keep their local ports, so the front proxy and the children agree.
const (
	natsHost     = "127.0.0.1"
	natsPort     = 4222
	natsStoreDir = "/data/nats"
	natsURL      = "nats://127.0.0.1:4222"

	minioDataDir = "/data/minio"
	minioAddr    = "127.0.0.1:9000"
	minioConsole = "127.0.0.1:9001"
	minioHealth  = "http://127.0.0.1:9000/minio/health/ready"

	apiUpstream = "http://127.0.0.1:10010"
	hubUpstream = "http://127.0.0.1:10011"

	defaultPort          = 8000
	natsReadyTimeout     = 10 * time.Second
	minioReadyTimeout    = 60 * time.Second
	proxyShutdownTimeout = 5 * time.Second
	proxyReadHeader      = 10 * time.Second
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Printf("fleetops-cloud: %v", err)
		os.Exit(1)
	}
}

// run boots the container and returns after ctx is cancelled and shutdown
// completes. Boot order: checks, NATS, MinIO (ready), children, front proxy.
func run(ctx context.Context) error {
	port, err := bootChecks(os.LookupEnv)
	if err != nil {
		return err
	}
	for _, dir := range []string{natsStoreDir, minioDataDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	nsrv, err := startNATS(natsHost, natsPort, natsStoreDir)
	if err != nil {
		return err
	}
	sup := newSupervisor()
	defer func() {
		// Children stop before NATS, since they need it to exit cleanly.
		sup.shutdown()
		nsrv.Shutdown()
		nsrv.WaitForShutdown()
	}()

	if err := startChildren(ctx, sup); err != nil {
		return err
	}
	if err := waitMinIOReady(ctx, minioHealth, minioReadyTimeout); err != nil {
		return err
	}
	// The api and ingest need MinIO and NATS, so they start after MinIO is ready.
	if err := startAppChildren(ctx, sup); err != nil {
		return err
	}

	apiURL, _ := url.Parse(apiUpstream)
	hubURL, _ := url.Parse(hubUpstream)
	srv := &http.Server{
		Addr:              "0.0.0.0:" + strconv.Itoa(port),
		Handler:           cloudproxy.New(apiURL, hubURL),
		ReadHeaderTimeout: proxyReadHeader,
	}
	proxyErr := make(chan error, 1)
	go func() { proxyErr <- srv.ListenAndServe() }()
	log.Printf("fleetops-cloud: listening on port %d", port)

	natsDown := watchNATS(ctx, nsrv)
	select {
	case <-ctx.Done():
		log.Printf("fleetops-cloud: shutting down")
	case err := <-proxyErr:
		return fmt.Errorf("front proxy: %w", err)
	case <-natsDown:
		return errors.New("embedded NATS stopped")
	}

	// The front proxy stops first, so no new request reaches a child that is
	// about to exit. Children and NATS then follow in the deferred shutdown.
	shutCtx, cancel := context.WithTimeout(context.Background(), proxyShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("fleetops-cloud: proxy shutdown: %v", err)
	}
	return nil
}

// bootChecks validates the cloud env before anything starts (GF-349). It returns
// the public port. Errors name the variable and never its value.
func bootChecks(lookup func(string) (string, bool)) (int, error) {
	if v, _ := lookup("FANOUT_MODE"); v != "nats" {
		return 0, errors.New("FANOUT_MODE must be nats in the cloud container")
	}
	for _, name := range []string{"DATABASE_URL", "JWT_SECRET", "DEVICE_KEY_SALT"} {
		if v, _ := lookup(name); v == "" {
			return 0, fmt.Errorf("%s is required", name)
		}
	}
	if v, _ := lookup("MINIO_KEY"); len(v) < 3 {
		return 0, errors.New("MINIO_KEY must be at least 3 bytes")
	}
	if v, _ := lookup("MINIO_SECRET"); len(v) < 8 {
		return 0, errors.New("MINIO_SECRET must be at least 8 bytes")
	}
	v, _ := lookup("PORT")
	if v == "" {
		return defaultPort, nil
	}
	port, err := strconv.Atoi(v)
	if err != nil || port < 1 || port > 65535 {
		return 0, errors.New("PORT must be an integer from 1 to 65535")
	}
	return port, nil
}

// startNATS runs the embedded server with JetStream (GF-350). It returns once
// the server accepts connections, or an error after the ready timeout.
func startNATS(host string, port int, storeDir string) (*natsserver.Server, error) {
	srv, err := natsserver.NewServer(&natsserver.Options{
		Host:      host,
		Port:      port,
		JetStream: true,
		StoreDir:  storeDir,
		NoSigs:    true,
	})
	if err != nil {
		return nil, fmt.Errorf("embedded nats: %w", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(natsReadyTimeout) {
		srv.Shutdown()
		return nil, errors.New("embedded nats not ready")
	}
	return srv, nil
}

// watchNATS closes the returned channel when the embedded server stops on its
// own. NATS is not restarted, so that is fatal (GF-349).
func watchNATS(ctx context.Context, srv *natsserver.Server) <-chan struct{} {
	down := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if !srv.Running() {
					close(down)
					return
				}
			}
		}
	}()
	return down
}

// startChildren starts MinIO first. It is the last child to stop (GF-350).
func startChildren(ctx context.Context, sup *supervisor) error {
	minioBin, err := resolve("minio")
	if err != nil {
		return err
	}
	env := append(childEnv(os.Environ()),
		"MINIO_ROOT_USER="+os.Getenv("MINIO_KEY"),
		"MINIO_ROOT_PASSWORD="+os.Getenv("MINIO_SECRET"),
		"MINIO_BROWSER=off",
	)
	sup.start(ctx, child{
		name: "minio",
		path: minioBin,
		args: []string{"server", minioDataDir, "--address", minioAddr, "--console-address", minioConsole},
		env:  env,
	})
	return nil
}

// startAppChildren starts the api, ingest and ws-hub in that order (GF-349).
func startAppChildren(ctx context.Context, sup *supervisor) error {
	env := childEnv(os.Environ())
	for _, name := range []string{"fleetops-api", "fleetops-ingest", "fleetops-ws-hub"} {
		path, err := resolve(name)
		if err != nil {
			return err
		}
		sup.start(ctx, child{name: name, path: path, env: env})
	}
	return nil
}

// childEnv overrides the env the children see (GF-349): REDIS_URL is removed,
// and NATS, MinIO and the fanout mode point at the container's own services.
func childEnv(base []string) []string {
	drop := map[string]bool{"REDIS_URL": true, "NATS_URL": true, "MINIO_ENDPOINT": true, "FANOUT_MODE": true}
	var out []string
	for _, kv := range base {
		key, _, _ := strings.Cut(kv, "=")
		if !drop[key] {
			out = append(out, kv)
		}
	}
	return append(out,
		"NATS_URL="+natsURL,
		"MINIO_ENDPOINT="+minioAddr,
		"FANOUT_MODE=nats",
	)
}

// waitMinIOReady polls the MinIO health endpoint until it answers 200 or the
// timeout passes (GF-350). Errors never include credentials.
func waitMinIOReady(ctx context.Context, healthURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("minio not ready within %s", timeout)
		}
		if err := sleepCtx(ctx, 250*time.Millisecond); err != nil {
			return err
		}
	}
}
