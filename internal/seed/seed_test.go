package seed_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"fleetops/internal/seed"
	"fleetops/internal/testinfra"
)

const goodPW = "correct horse battery"

func validOpts() seed.Options {
	return seed.Options{AdminEmail: "admin@example.com", FleetName: "FleetOps", Timezone: "UTC"}
}

func constPW(pw string) seed.PasswordSource {
	return func() (string, error) { return pw, nil }
}

func TestValidateRejectsBadEmail(t *testing.T) {
	for _, email := range []string{"", "not-an-email", "Name <a@example.com>", "a@"} {
		o := validOpts()
		o.AdminEmail = email
		if _, err := seed.Validate(o); !errors.Is(err, seed.ErrUsage) {
			t.Errorf("email %q: want ErrUsage, got %v", email, err)
		}
	}
}

func TestValidateRejectsBadTimezoneAndEmptyFleetName(t *testing.T) {
	o := validOpts()
	o.Timezone = "Mars/Olympus"
	if _, err := seed.Validate(o); !errors.Is(err, seed.ErrUsage) {
		t.Errorf("bad timezone: want ErrUsage, got %v", err)
	}
	o = validOpts()
	o.FleetName = "   "
	if _, err := seed.Validate(o); !errors.Is(err, seed.ErrUsage) {
		t.Errorf("empty fleet name: want ErrUsage, got %v", err)
	}
}

func TestValidateNormalizesAndAcceptsIANAZone(t *testing.T) {
	got, err := seed.Validate(seed.Options{
		AdminEmail: "Admin@Example.COM", FleetName: "  Istanbul Fleet ", Timezone: "Europe/Istanbul",
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got.AdminEmail != "admin@example.com" || got.FleetName != "Istanbul Fleet" || got.Timezone != "Europe/Istanbul" {
		t.Fatalf("normalized = %+v", got)
	}
}

// Validation runs before any database access: a nil handle would panic otherwise.
func TestRunValidatesBeforeTouchingDatabase(t *testing.T) {
	o := validOpts()
	o.Timezone = "Nowhere/Land"
	_, err := seed.Run(context.Background(), nil, o, constPW(goodPW))
	if !errors.Is(err, seed.ErrUsage) {
		t.Fatalf("want ErrUsage before any DB access, got %v", err)
	}
}

func TestFirstRunCreatesOneFleetAndOneAdmin(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	o := validOpts()
	o.Timezone = "Europe/Istanbul"

	res, err := seed.Run(context.Background(), db, o, constPW(goodPW))
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if !res.Created || res.FleetID == 0 || res.UserID == 0 {
		t.Fatalf("result = %+v, want created", res)
	}

	var fleets, users, adminRole, driverNull, audit int
	var tz string
	mustScan(t, db, `SELECT count(*), max(timezone) FROM fleets`, &fleets, &tz)
	mustScan(t, db, `SELECT count(*) FROM users WHERE role = 'admin' AND driver_id IS NULL`, &adminRole)
	mustScan(t, db, `SELECT count(*) FROM users`, &users)
	mustScan(t, db, `SELECT count(*) FROM users WHERE driver_id IS NULL`, &driverNull)
	mustScan(t, db, `SELECT count(*) FROM admin_audit`, &audit)

	if fleets != 1 || users != 1 || adminRole != 1 || driverNull != 1 {
		t.Fatalf("fleets=%d users=%d admin=%d driverNull=%d", fleets, users, adminRole, driverNull)
	}
	if tz != "Europe/Istanbul" {
		t.Fatalf("timezone = %q", tz)
	}
	if audit != 0 {
		t.Fatalf("bootstrap wrote %d audit rows, want 0 (no actor yet)", audit)
	}
}

func TestSecondRunIsNoOpAndDoesNotPrompt(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	if _, err := seed.Run(context.Background(), db, validOpts(), constPW(goodPW)); err != nil {
		t.Fatalf("first run: %v", err)
	}

	var prompts atomic.Int32
	pw := func() (string, error) {
		prompts.Add(1)
		return goodPW, nil
	}
	res, err := seed.Run(context.Background(), db, validOpts(), pw)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if res.Created {
		t.Fatal("second run created rows")
	}
	if prompts.Load() != 0 {
		t.Fatalf("second run prompted %d times, want 0", prompts.Load())
	}
	var fleets, users int
	mustScan(t, db, `SELECT count(*) FROM fleets`, &fleets)
	mustScan(t, db, `SELECT count(*) FROM users`, &users)
	if fleets != 1 || users != 1 {
		t.Fatalf("after second run fleets=%d users=%d, want 1 and 1", fleets, users)
	}
}

func TestWeakPasswordRejectedBeforeAnyWrite(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	_, err := seed.Run(context.Background(), db, validOpts(), constPW("short"))
	if !errors.Is(err, seed.ErrUsage) {
		t.Fatalf("weak password: want ErrUsage, got %v", err)
	}
	var fleets int
	mustScan(t, db, `SELECT count(*) FROM fleets`, &fleets)
	if fleets != 0 {
		t.Fatalf("weak password still wrote %d fleets", fleets)
	}
}

func TestConcurrentRunsCreateOneFleet(t *testing.T) {
	db := testinfra.NewMigratedDB(t)

	var wg sync.WaitGroup
	results := make([]seed.Result, 2)
	errs := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = seed.Run(context.Background(), db, validOpts(), constPW(goodPW))
		}(i)
	}
	wg.Wait()

	created := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("run %d: %v", i, errs[i])
		}
		if results[i].Created {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created in %d runs, want exactly 1", created)
	}
	var fleets, users int
	mustScan(t, db, `SELECT count(*) FROM fleets`, &fleets)
	mustScan(t, db, `SELECT count(*) FROM users`, &users)
	if fleets != 1 || users != 1 {
		t.Fatalf("fleets=%d users=%d, want 1 and 1", fleets, users)
	}
}

func mustScan(t *testing.T, db *sql.DB, query string, dest ...any) {
	t.Helper()
	if err := db.QueryRow(query).Scan(dest...); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
}
