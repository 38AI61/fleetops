package seed_test

import (
	"context"
	"testing"

	"fleetops/internal/seed"
	"fleetops/internal/testinfra"
)

// TestSeedWritesThreeAlertRules: the first seed writes the three D-08 rule rows
// for the new fleet inside the seed transaction. A rerun adds none (GF-290).
func TestSeedWritesThreeAlertRules(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	ctx := context.Background()
	res, err := seed.Run(ctx, db, validOpts(), constPW(goodPW))
	if err != nil || !res.Created {
		t.Fatalf("Run = %+v, %v; want created", res, err)
	}
	if n := count(t, db, `SELECT count(*) FROM alert_rules WHERE fleet_id = $1`, res.FleetID); n != 3 {
		t.Fatalf("alert rules = %d, want 3", n)
	}
	if n := count(t, db, `SELECT count(*) FROM alert_rules WHERE fleet_id = $1 AND kind = 'eta_slip' AND enabled = false`, res.FleetID); n != 1 {
		t.Fatalf("disabled eta_slip rows = %d, want 1", n)
	}

	if _, err := seed.Run(ctx, db, validOpts(), constPW(goodPW)); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if n := count(t, db, `SELECT count(*) FROM alert_rules`); n != 3 {
		t.Fatalf("alert rules after rerun = %d, want 3", n)
	}
}
