package eta

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"fleetops/internal/alerts"
)

// SlipThreshold is how far past window_end the projected arrival may be before the rule fires (D-34).
const SlipThreshold = 15 * time.Minute

// Slip reports whether now + etaMinutes is strictly after window_end + SlipThreshold (D-34, GF-310).
// A job with no window never slips.
func Slip(now time.Time, etaMinutes int, windowEnd *time.Time) bool {
	if windowEnd == nil {
		return false
	}
	projected := now.Add(time.Duration(etaMinutes) * time.Minute)
	return projected.After(windowEnd.Add(SlipThreshold))
}

// slipPayload is the eta_slip alert payload (GF-312). Times are RFC3339 in UTC.
type slipPayload struct {
	JobID        int64  `json:"job_id"`
	VehicleID    int64  `json:"vehicle_id"`
	ETAMinutes   int    `json:"eta_minutes"`
	ProjectedAt  string `json:"projected_at"`
	WindowEnd    string `json:"window_end"`
	ThresholdMin int    `json:"threshold_min"`
}

// loadSlipRules reads the eta_slip enabled flag per fleet, once per pass with no cache (GF-297, GF-313).
func loadSlipRules(ctx context.Context, tx *sql.Tx) (map[int64]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT fleet_id, enabled FROM alert_rules WHERE kind = $1`, alerts.KindETASlip)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	on := make(map[int64]bool)
	for rows.Next() {
		var fleetID int64
		var enabled bool
		if err := rows.Scan(&fleetID, &enabled); err != nil {
			return nil, err
		}
		on[fleetID] = enabled
	}
	return on, rows.Err()
}

// raiseSlip inserts one eta_slip alert for the job's window, if the window has none yet (GF-311).
// It returns the raised alert, or nil when the job is not slipping or already alerted for this window.
// The pass holds the advisory lock, so the NOT EXISTS check cannot race (GF-294).
func raiseSlip(ctx context.Context, tx *sql.Tx, c candidate, minutes int, now time.Time) (*alerts.Raised, error) {
	if !Slip(now, minutes, c.windowEnd) {
		return nil, nil
	}
	windowEnd := c.windowEnd.UTC().Format(time.RFC3339)

	var seen bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM alerts
		  WHERE kind = $1 AND payload->>'job_id' = $2 AND payload->>'window_end' = $3)`,
		alerts.KindETASlip, fmt.Sprint(c.id), windowEnd,
	).Scan(&seen); err != nil {
		return nil, fmt.Errorf("eta_slip dedup for job %d: %w", c.id, err)
	}
	if seen {
		return nil, nil
	}

	body, err := json.Marshal(slipPayload{
		JobID:        c.id,
		VehicleID:    c.vehicleID,
		ETAMinutes:   minutes,
		ProjectedAt:  now.Add(time.Duration(minutes) * time.Minute).UTC().Format(time.RFC3339),
		WindowEnd:    windowEnd,
		ThresholdMin: int(SlipThreshold / time.Minute),
	})
	if err != nil {
		return nil, err
	}
	a, err := alerts.RaiseETASlip(ctx, tx, c.fleetID, body)
	if err != nil {
		return nil, fmt.Errorf("raise eta_slip for job %d: %w", c.id, err)
	}
	return &a, nil
}
