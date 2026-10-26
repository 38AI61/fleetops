// Command simulator drives the demo devices against POST /ingest/position at a
// sustained per-vehicle rate (D-12, P8). It reads deploy/.local/devices.json
// and touches no database, NATS, or Redis. Logic lives in internal/simulator.
//
// Exit codes: 0 normal or signal-initiated shutdown, 1 runtime (devices file
// missing, unreadable, or invalid), 2 usage (flags, bounds, or API URL).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"syscall"

	"fleetops/internal/devicesfile"
	"fleetops/internal/simulator"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("simulator", flag.ContinueOnError)
	flags.SetOutput(stderr)
	fleet := flags.Int("fleet", simulator.DefaultFleet, "number of simulated vehicles, the first N in the devices file (1 to the file's device count)")
	rate := flags.Duration("rate", simulator.DefaultRate, "one position per vehicle per interval (500ms to 60s)")
	api := flags.String("api", simulator.DefaultAPI, "API base URL (http or https)")
	devices := flags.String("devices", devicesfile.DefaultPath, "devices file written by tools/seed --demo-devices")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "simulator: unexpected argument %q\n", flags.Arg(0))
		return 2
	}
	if *fleet < 1 {
		_, _ = fmt.Fprintln(stderr, "simulator: --fleet must be at least 1")
		return 2
	}
	if err := simulator.CheckRate(*rate); err != nil {
		_, _ = fmt.Fprintf(stderr, "simulator: %v\n", err)
		return 2
	}
	apiURL, err := simulator.NormalizeAPI(*api)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "simulator: %v\n", err)
		return 2
	}

	file, err := devicesfile.Read(*devices)
	if errors.Is(err, fs.ErrNotExist) {
		_, _ = fmt.Fprintf(stderr, "simulator: %s not found; seed the demo devices first: SEED_ARGS=\"--admin-email <email> --demo-devices\" make seed\n", *devices)
		return 1
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "simulator: %v\n", err)
		return 1
	}
	vehicles, err := simulator.Vehicles(file, *fleet)
	if errors.Is(err, simulator.ErrUsage) {
		_, _ = fmt.Fprintf(stderr, "simulator: %v\n", err)
		return 2
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "simulator: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sum, err := simulator.Run(ctx, simulator.Config{
		Vehicles: vehicles,
		Rate:     *rate,
		Sender:   simulator.NewHTTPSender(apiURL, *fleet),
		Log:      stderr,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "simulator: %v\n", err)
		return 1
	}
	t := sum.Total
	_, _ = fmt.Fprintf(stderr, "simulator: sent=%d accepted=%d rejected=%d failed=%d skipped=%d\n",
		t.Sent, t.Accepted, t.Rejected, t.Failed, t.Skipped)
	return 0
}
