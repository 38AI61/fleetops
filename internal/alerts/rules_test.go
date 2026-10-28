package alerts

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestValidateParams: device_silent takes threshold_s, an integer in 60..86400;
// the other kinds take {} only (GF-298).
func TestValidateParams(t *testing.T) {
	cases := []struct {
		name string
		kind string
		raw  string
		ok   bool
	}{
		{"silent min", KindDeviceSilent, `{"threshold_s":60}`, true},
		{"silent max", KindDeviceSilent, `{"threshold_s":86400}`, true},
		{"silent below min", KindDeviceSilent, `{"threshold_s":59}`, false},
		{"silent above max", KindDeviceSilent, `{"threshold_s":86401}`, false},
		{"silent fraction", KindDeviceSilent, `{"threshold_s":600.5}`, false},
		{"silent quoted number", KindDeviceSilent, `{"threshold_s":"600"}`, false},
		{"silent missing key", KindDeviceSilent, `{}`, false},
		{"silent extra key", KindDeviceSilent, `{"threshold_s":600,"x":1}`, false},
		{"silent not an object", KindDeviceSilent, `[600]`, false},
		{"silent null", KindDeviceSilent, `null`, false},
		{"eta empty", KindETASlip, `{}`, true},
		{"eta with key", KindETASlip, `{"x":1}`, false},
		{"schedule empty", KindGeofenceExit, `{}`, true},
		{"schedule with key", KindGeofenceExit, `{"threshold_s":600}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ValidateParams(c.kind, json.RawMessage(c.raw))
			if (err == nil) != c.ok {
				t.Fatalf("ValidateParams(%s, %s) err = %v, want ok=%v", c.kind, c.raw, err, c.ok)
			}
			if err != nil && !strings.Contains(err.Error(), ErrValidation.Error()) {
				t.Fatalf("error %v does not wrap ErrValidation", err)
			}
		})
	}
}

// TestParsePatchRejects: unknown kind, duplicate kind, more than three entries,
// and unknown keys are validation errors (GF-298).
func TestParsePatchRejects(t *testing.T) {
	bad := map[string]string{
		"unknown kind":     `{"rules":[{"kind":"eta_slip_x"}]}`,
		"missing kind":     `{"rules":[{"enabled":true}]}`,
		"duplicate kind":   `{"rules":[{"kind":"eta_slip"},{"kind":"eta_slip"}]}`,
		"four entries":     `{"rules":[{"kind":"eta_slip"},{"kind":"device_silent"},{"kind":"geofence_exit_outside_schedule"},{"kind":"eta_slip"}]}`,
		"unknown key":      `{"rules":[{"kind":"eta_slip","x":1}]}`,
		"enabled not bool": `{"rules":[{"kind":"eta_slip","enabled":"yes"}]}`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := ParsePatch(strings.NewReader(body)); err == nil {
				t.Fatalf("ParsePatch(%s) accepted", body)
			}
		})
	}
}
