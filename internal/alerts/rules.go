// Package alerts is the P19 alert core: the per-fleet rules (D-08), the
// append-only alerts table with its one ack transition (D-09), the 30 s
// evaluator for device_silent and geofence_exit_outside_schedule (D-34), the
// alert.raised fanout and the Notifier stub (D-11).
package alerts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// ErrValidation marks a body or params the server refuses (GF-298). The HTTP
// layer answers 400 validation_failed and returns no reason.
var ErrValidation = errors.New("alerts: validation failed")

// The three fixed kinds (D-08). Kinds are not configurable; parameters are.
// eta_slip has no evaluator in P19 (GF-290): it is seeded disabled and only
// appears in the rule list.
const (
	KindDeviceSilent = "device_silent"
	KindETASlip      = "eta_slip"
	KindGeofenceExit = "geofence_exit_outside_schedule"
)

// Kinds lists the fixed kinds in seed order (D-08).
var Kinds = []string{KindDeviceSilent, KindETASlip, KindGeofenceExit}

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

// Bounds for device_silent.threshold_s (GF-298).
const (
	minThresholdS = 60
	maxThresholdS = 86400
)

// RulePatch is one validated entry of PATCH /alert-rules (GF-298). Nil fields are
// left as they are. Params is the canonical JSON for the kind.
type RulePatch struct {
	Kind    string
	Enabled *bool
	Params  json.RawMessage
}

// ruleIn mirrors one posted entry. The decoder rejects unknown keys (GF-298).
type ruleIn struct {
	Kind    *string         `json:"kind"`
	Enabled *bool           `json:"enabled"`
	Params  json.RawMessage `json:"params"`
}

type patchIn struct {
	Rules []ruleIn `json:"rules"`
}

// ParsePatch decodes and validates a PATCH /alert-rules body. At most one entry
// per kind and at most three entries (GF-298). Every failure wraps ErrValidation.
func ParsePatch(r io.Reader) ([]RulePatch, error) {
	var in patchIn
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("%w: body", ErrValidation)
	}
	if len(in.Rules) > len(Kinds) {
		return nil, fmt.Errorf("%w: at most %d rules", ErrValidation, len(Kinds))
	}
	seen := make(map[string]bool, len(in.Rules))
	out := make([]RulePatch, 0, len(in.Rules))
	for _, e := range in.Rules {
		if e.Kind == nil || !isKind(*e.Kind) {
			return nil, fmt.Errorf("%w: unknown kind", ErrValidation)
		}
		if seen[*e.Kind] {
			return nil, fmt.Errorf("%w: duplicate kind", ErrValidation)
		}
		seen[*e.Kind] = true
		var params json.RawMessage
		if len(e.Params) > 0 {
			p, err := ValidateParams(*e.Kind, e.Params)
			if err != nil {
				return nil, err
			}
			params = p
		}
		out = append(out, RulePatch{Kind: *e.Kind, Enabled: e.Enabled, Params: params})
	}
	return out, nil
}

// ValidateParams checks one kind's params (GF-298) and returns the canonical JSON.
// device_silent takes exactly threshold_s, an integer in 60..86400. The other
// kinds take {} only.
func ValidateParams(kind string, raw json.RawMessage) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	// Values decode as any, not json.Number: json.Number also accepts a quoted number.
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, fmt.Errorf("%w: params must be an object", ErrValidation)
	}
	if kind != KindDeviceSilent {
		if len(m) != 0 {
			return nil, fmt.Errorf("%w: %s takes no params", ErrValidation, kind)
		}
		return json.RawMessage(`{}`), nil
	}
	if len(m) != 1 {
		return nil, fmt.Errorf("%w: device_silent takes threshold_s only", ErrValidation)
	}
	n, ok := m["threshold_s"].(json.Number)
	if !ok {
		return nil, fmt.Errorf("%w: threshold_s must be a number", ErrValidation)
	}
	v, err := strconv.ParseInt(n.String(), 10, 64)
	if err != nil || v < minThresholdS || v > maxThresholdS {
		return nil, fmt.Errorf("%w: threshold_s out of range", ErrValidation)
	}
	return json.RawMessage(fmt.Sprintf(`{"threshold_s": %d}`, v)), nil
}

// isKind reports whether s is one of the three fixed kinds.
func isKind(s string) bool {
	for _, k := range Kinds {
		if k == s {
			return true
		}
	}
	return false
}
