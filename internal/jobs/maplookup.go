package jobs

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lib/pq"

	"fleetops/internal/mapview"
)

// MapLookup implements mapview.JobLookup from the jobs table (GF-133, GF-181).
type MapLookup struct {
	DB *sql.DB
}

var _ mapview.JobLookup = (*MapLookup)(nil)

// activeJobSQL picks one active job per vehicle, en_route first, then picked_up,
// accepted and assigned (GF-181).
const activeJobSQL = `
SELECT DISTINCT ON (vehicle_id) vehicle_id, state
FROM jobs
WHERE fleet_id = $1 AND vehicle_id = ANY($2)
  AND state IN ('assigned','accepted','en_route','picked_up')
ORDER BY vehicle_id,
  CASE state WHEN 'en_route' THEN 0 WHEN 'picked_up' THEN 1 WHEN 'accepted' THEN 2 ELSE 3 END`

// ActiveJobStates reports the map state of each vehicle's active job. A vehicle
// with no active job is absent from the result (JobNone).
func (m *MapLookup) ActiveJobStates(ctx context.Context, fleetID int64, vehicleIDs []int64) (map[int64]mapview.JobState, error) {
	out := map[int64]mapview.JobState{}
	if len(vehicleIDs) == 0 {
		return out, nil
	}
	rows, err := m.DB.QueryContext(ctx, activeJobSQL, fleetID, pq.Array(vehicleIDs))
	if err != nil {
		return nil, fmt.Errorf("active job states: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var vehicle int64
		var state string
		if err := rows.Scan(&vehicle, &state); err != nil {
			return nil, fmt.Errorf("scan active job: %w", err)
		}
		out[vehicle] = mapState(State(state))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("active job rows: %w", err)
	}
	return out, nil
}

// mapState is the display mapping in GF-181: picked_up shows as en_route.
func mapState(s State) mapview.JobState {
	if s == StateEnRoute || s == StatePickedUp {
		return mapview.JobEnRoute
	}
	return mapview.JobOther
}
