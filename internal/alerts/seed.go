package alerts

import (
	"context"
	"database/sql"
	"fmt"
)

// SeedRules inserts the three fixed rule rows for one fleet (D-08). It runs
// inside the seed transaction after the fleet insert (GF-290). ON CONFLICT
// keeps a second call a no-op.
func SeedRules(ctx context.Context, tx *sql.Tx, fleetID int64) error {
	for _, r := range defaultRules {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO alert_rules (fleet_id, kind, params, enabled)
			VALUES ($1, $2, $3::jsonb, $4)
			ON CONFLICT (fleet_id, kind) DO NOTHING`,
			fleetID, r.Kind, r.Params, r.Enabled,
		); err != nil {
			return fmt.Errorf("seed alert rule %s: %w", r.Kind, err)
		}
	}
	return nil
}
