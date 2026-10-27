package jobs

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestCreateValidators is the unit table for the GF-173 create bounds.
func TestCreateValidators(t *testing.T) {
	pts := `"pickup":{"lat":41.0,"lng":29.0},"dropoff":{"lat":41.1,"lng":29.1}`
	long := strings.Repeat("n", maxNotesRunes+1)
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{"minimal", `{` + pts + `}`, true},
		{"full", `{` + pts + `,"window_start":"2026-10-26T09:00:00Z","window_end":"2026-10-26T12:00:00+02:00","eta_minutes":0,"notes":"gate 4","size_class":"large","weight_kg":0.5}`, true},
		{"size_class null", `{` + pts + `,"size_class":null}`, true},
		{"eta 1440", `{` + pts + `,"eta_minutes":1440}`, true},
		{"notes exactly 1000 runes", `{` + pts + `,"notes":"` + strings.Repeat("ö", maxNotesRunes) + `"}`, true},
		{"missing pickup", `{"dropoff":{"lat":41,"lng":29}}`, false},
		{"missing dropoff", `{"pickup":{"lat":41,"lng":29}}`, false},
		{"point without lng", `{"pickup":{"lat":41},"dropoff":{"lat":41,"lng":29}}`, false},
		{"lat 91", `{"pickup":{"lat":91,"lng":29},"dropoff":{"lat":41,"lng":29}}`, false},
		{"lng -181", `{"pickup":{"lat":41,"lng":-181},"dropoff":{"lat":41,"lng":29}}`, false},
		{"window_end equal to start", `{` + pts + `,"window_start":"2026-10-26T09:00:00Z","window_end":"2026-10-26T09:00:00Z"}`, false},
		{"window_end before start", `{` + pts + `,"window_start":"2026-10-26T09:00:00Z","window_end":"2026-10-26T08:00:00Z"}`, false},
		{"window without zone", `{` + pts + `,"window_start":"2026-10-26T09:00:00"}`, false},
		{"eta -1", `{` + pts + `,"eta_minutes":-1}`, false},
		{"eta 1441", `{` + pts + `,"eta_minutes":1441}`, false},
		{"notes 1001 runes", `{` + pts + `,"notes":"` + long + `"}`, false},
		{"size_class huge", `{` + pts + `,"size_class":"huge"}`, false},
		{"size_class empty", `{` + pts + `,"size_class":""}`, false},
		{"weight 0", `{` + pts + `,"weight_kg":0}`, false},
		{"weight negative", `{` + pts + `,"weight_kg":-1}`, false},
		{"weight over 100000", `{` + pts + `,"weight_kg":100000.01}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var in createBody
			if err := json.Unmarshal([]byte(tc.body), &in); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			_, err := newJobFrom(in)
			if tc.ok && err != nil {
				t.Fatalf("newJobFrom: %v, want ok", err)
			}
			if !tc.ok && !errors.Is(err, ErrValidation) {
				t.Fatalf("newJobFrom: %v, want ErrValidation", err)
			}
		})
	}
}
