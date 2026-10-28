// Package alerts is the P19 alert core: the per-fleet rules (D-08), the
// append-only alerts table with its one ack transition (D-09), the 30 s
// evaluator for device_silent and geofence_exit_outside_schedule (D-34), the
// alert.raised fanout and the Notifier stub (D-11).
package alerts

// The three fixed kinds (D-08). Kinds are not configurable; parameters are.
// eta_slip has no evaluator in P19 (GF-290): it is seeded disabled and only
// appears in the rule list.
const (
	KindDeviceSilent = "device_silent"
	KindETASlip      = "eta_slip"
	KindGeofenceExit = "geofence_exit_outside_schedule"
)

// Severities of an alert (D-34). device_silent is a warning; a schedule exit is critical.
const (
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// DefaultThresholdS is the device_silent default: ten minutes (D-34, GF-297).
const DefaultThresholdS = 600

// defaultRule is one seeded alert_rules row (D-08, GF-290, GF-297).
type defaultRule struct {
	Kind    string
	Params  string
	Enabled bool
}

// defaultRules is the per-fleet seed and the backfill in 011_alerts_core.up.sql.
// Both must stay in step with each other.
var defaultRules = []defaultRule{
	{Kind: KindDeviceSilent, Params: `{"threshold_s": 600}`, Enabled: true},
	{Kind: KindETASlip, Params: `{}`, Enabled: false},
	{Kind: KindGeofenceExit, Params: `{}`, Enabled: true},
}
