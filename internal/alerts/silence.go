package alerts

import "time"

// decideSilence reports whether a device_silent alert is due for one vehicle
// (GF-292). It is due when now - lastSeen is strictly greater than threshold,
// and the episode key (vehicle, lastSeen) has no alert yet. existing is that
// key's presence in alerts. Silence is measured from the report's server_ts.
// The function holds no state: a new report changes lastSeen, so it is a new episode.
func decideSilence(lastSeen, now time.Time, threshold time.Duration, existing bool) bool {
	return now.Sub(lastSeen) > threshold && !existing
}
