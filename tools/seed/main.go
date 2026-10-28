// Command seed bootstraps the first fleet and admin (D-05). It is run by
// `make seed`. The password comes from a no-echo TTY prompt or one line on
// stdin, never from a flag or the environment (GF-23). With --demo-devices it
// also writes the demo devices and demo driver logins on first seed (GF-135, GF-138, GF-339).
//
// Exit codes: 0 seeded or already seeded, 1 runtime error, 2 usage error.
package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	_ "github.com/lib/pq"
	"golang.org/x/term"

	"fleetops/internal/devicesfile"
	"fleetops/internal/seed"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	fs.SetOutput(stderr)
	adminEmail := fs.String("admin-email", "", "email of the first admin (required)")
	fleetName := fs.String("fleet-name", "FleetOps", "name of the first fleet")
	timezone := fs.String("timezone", "UTC", "IANA timezone of the first fleet")
	demoDevices := fs.Bool("demo-devices", false, "on first seed, also create demo vehicles, tracker devices and demo drivers (needs DEVICE_KEY_SALT)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	opts := seed.Options{
		AdminEmail:  *adminEmail,
		FleetName:   *fleetName,
		Timezone:    *timezone,
		DemoDevices: *demoDevices,
		DevicesPath: devicesfile.DefaultPath,
	}
	if *demoDevices {
		// The salt is read only when the flag is set (GF-135). It is never printed.
		opts.DeviceKeySalt = []byte(os.Getenv("DEVICE_KEY_SALT"))
	}
	if strings.TrimSpace(opts.AdminEmail) == "" {
		_, _ = fmt.Fprintln(stderr, "seed: --admin-email is required")
		return 2
	}
	norm, err := seed.Validate(opts)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "seed: %v\n", err)
		return 2
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		_, _ = fmt.Fprintln(stderr, "seed: DATABASE_URL is required")
		return 2
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "seed: open database: %v\n", err)
		return 1
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "seed: ping database: %v\n", err)
		return 1
	}

	res, err := seed.Run(ctx, db, opts, passwordSource(stdin, stderr))
	if errors.Is(err, seed.ErrUsage) {
		_, _ = fmt.Fprintf(stderr, "seed: %v\n", err)
		return 2
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "seed: %v\n", err)
		return 1
	}
	if !res.Created {
		_, _ = fmt.Fprintln(stdout, "already seeded; no changes")
		if *demoDevices {
			_, _ = fmt.Fprintf(stderr, "seed: --demo-devices applies only on first seed; %s was not rewritten\n", devicesfile.DefaultPath)
		}
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "seeded fleet %q (id %d) with admin %s (id %d)\n",
		norm.FleetName, res.FleetID, norm.AdminEmail, res.UserID)
	if *demoDevices {
		_, _ = fmt.Fprintf(stdout, "wrote %d demo devices to %s\n", seed.DemoVehicleCount, devicesfile.DefaultPath)
		_, _ = fmt.Fprintf(stdout, "wrote %d demo driver logins to %s\n", seed.DemoDriverCount, seed.DemoUsersDefaultPath)
	}
	return 0
}

// passwordSource reads the password from a terminal with echo off, asked
// twice, or else from one line on stdin.
func passwordSource(stdin io.Reader, stderr io.Writer) seed.PasswordSource {
	return func() (string, error) {
		if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
			return promptTwice(f, stderr)
		}
		return readLine(stdin)
	}
}

func promptTwice(f *os.File, stderr io.Writer) (string, error) {
	_, _ = fmt.Fprint(stderr, "Admin password: ")
	first, err := term.ReadPassword(int(f.Fd()))
	_, _ = fmt.Fprintln(stderr)
	if err != nil {
		return "", fmt.Errorf("%w: read password: %w", seed.ErrUsage, err)
	}
	_, _ = fmt.Fprint(stderr, "Repeat password: ")
	second, err := term.ReadPassword(int(f.Fd()))
	_, _ = fmt.Fprintln(stderr)
	if err != nil {
		return "", fmt.Errorf("%w: read password: %w", seed.ErrUsage, err)
	}
	if string(first) != string(second) {
		return "", fmt.Errorf("%w: passwords do not match", seed.ErrUsage)
	}
	return string(first), nil
}

func readLine(stdin io.Reader) (string, error) {
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("%w: read stdin: %w", seed.ErrUsage, err)
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", fmt.Errorf("%w: no password on stdin (pipe one line, or run from a terminal)", seed.ErrUsage)
	}
	return line, nil
}
