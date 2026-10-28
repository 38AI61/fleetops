package alerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// RaiseETASlip inserts one eta_slip warning inside the caller's transaction (D-34, GF-310).
// The ETA loop owns the dedup and the fleet's rule gate (GF-311, GF-313). This is a thin
// wrapper over insertAlert. The payload is the GF-312 shape, built by the caller.
func RaiseETASlip(ctx context.Context, tx *sql.Tx, fleetID int64, payload json.RawMessage) (Raised, error) {
	return insertAlert(ctx, tx, fleetID, KindETASlip, SeverityWarning, payload, time.Now().UTC())
}
