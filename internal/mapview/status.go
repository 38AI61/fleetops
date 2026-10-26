package mapview

import (
	"context"
	"time"
)

// JobState is the vehicle's active job state as the map needs it (D-27).
// JobNone means no active job.
type JobState string

// Job states from the D-27 vocabulary. Only en_route and picked_up change the display.
const (
	JobNone     JobState = ""
	JobEnRoute  JobState = "en_route"
	JobPickedUp JobState = "picked_up"
	JobOther    JobState = "other"
)

// DisplayStatus is the map status derived server-side (D-27, GF-130).
type DisplayStatus string

// Display statuses.
const (
	StatusIdle    DisplayStatus = "idle"
	StatusEnRoute DisplayStatus = "en_route"
	StatusOffline DisplayStatus = "offline"
)

// DeriveStatus applies D-27 in order (GF-131). A vehicle silent for longer than
// ttl is offline, even mid-job. Otherwise an en_route or picked_up job is
// en_route, and everything else is idle. A negative age (clock skew between
// the database and the API) counts as fresh.
func DeriveStatus(serverTS, now time.Time, ttl time.Duration, job JobState) DisplayStatus {
	if now.Sub(serverTS) > ttl {
		return StatusOffline
	}
	if job == JobEnRoute || job == JobPickedUp {
		return StatusEnRoute
	}
	return StatusIdle
}

// JobLookup supplies active job states for the vehicles on a map response
// (GF-133). P8 wires noJobLookup; the P10 jobs tables implement this interface.
type JobLookup interface {
	ActiveJobStates(ctx context.Context, fleetID int64, vehicleIDs []int64) (map[int64]JobState, error)
}

// noJobLookup reports no active jobs, so every fresh vehicle derives idle (GF-133).
type noJobLookup struct{}

func (noJobLookup) ActiveJobStates(context.Context, int64, []int64) (map[int64]JobState, error) {
	return map[int64]JobState{}, nil
}
