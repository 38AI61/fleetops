package ingest

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

// validRequest returns a body that passes every rule; each case edits one field.
func validRequest() PositionRequest {
	ts := testNow.Format(time.RFC3339)
	return PositionRequest{
		Lat: f64(41.0), Lng: f64(29.0), Speed: f64(50), Heading: f64(90),
		Accuracy: f64(8), TS: &ts,
	}
}

func f64(v float64) *float64 { return &v }

func str(s string) *string { return &s }

func TestValidatePosition(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*PositionRequest)
		field  string // "" means accepted
	}{
		{"lat min accepted", func(r *PositionRequest) { r.Lat = f64(-90) }, ""},
		{"lat max accepted", func(r *PositionRequest) { r.Lat = f64(90) }, ""},
		{"lat below min rejected", func(r *PositionRequest) { r.Lat = f64(-90.0001) }, "lat"},
		{"lat above max rejected", func(r *PositionRequest) { r.Lat = f64(90.0001) }, "lat"},
		{"lng min accepted", func(r *PositionRequest) { r.Lng = f64(-180) }, ""},
		{"lng max accepted", func(r *PositionRequest) { r.Lng = f64(180) }, ""},
		{"lng below min rejected", func(r *PositionRequest) { r.Lng = f64(-180.0001) }, "lng"},
		{"lng above max rejected", func(r *PositionRequest) { r.Lng = f64(180.0001) }, "lng"},
		{"speed min accepted", func(r *PositionRequest) { r.Speed = f64(0) }, ""},
		{"speed max accepted", func(r *PositionRequest) { r.Speed = f64(300) }, ""},
		{"speed below min rejected", func(r *PositionRequest) { r.Speed = f64(-0.5) }, "speed"},
		{"speed above max rejected", func(r *PositionRequest) { r.Speed = f64(300.5) }, "speed"},
		{"heading min accepted", func(r *PositionRequest) { r.Heading = f64(0) }, ""},
		{"heading max accepted", func(r *PositionRequest) { r.Heading = f64(360) }, ""},
		{"heading below min rejected", func(r *PositionRequest) { r.Heading = f64(-0.1) }, "heading"},
		{"heading above max rejected", func(r *PositionRequest) { r.Heading = f64(360.1) }, "heading"},
		{"accuracy min accepted", func(r *PositionRequest) { r.Accuracy = f64(0) }, ""},
		{"accuracy max accepted", func(r *PositionRequest) { r.Accuracy = f64(10000) }, ""},
		{"accuracy below min rejected", func(r *PositionRequest) { r.Accuracy = f64(-1) }, "accuracy"},
		{"accuracy above max rejected", func(r *PositionRequest) { r.Accuracy = f64(10000.5) }, "accuracy"},
		{"accuracy 50 m is kept (P5 drops it, not P4)", func(r *PositionRequest) { r.Accuracy = f64(51) }, ""},
		{"lat missing", func(r *PositionRequest) { r.Lat = nil }, "lat"},
		{"lng missing", func(r *PositionRequest) { r.Lng = nil }, "lng"},
		{"speed missing", func(r *PositionRequest) { r.Speed = nil }, "speed"},
		{"heading missing", func(r *PositionRequest) { r.Heading = nil }, "heading"},
		{"accuracy missing", func(r *PositionRequest) { r.Accuracy = nil }, "accuracy"},
		{"ts missing", func(r *PositionRequest) { r.TS = nil }, "ts"},
		{"ts not RFC3339", func(r *PositionRequest) { r.TS = str("2026-10-25 12:00:00") }, "ts"},
		{"ts date only", func(r *PositionRequest) { r.TS = str("2026-10-25") }, "ts"},
		{"ts at now+5m accepted", func(r *PositionRequest) { r.TS = str(testNow.Add(5 * time.Minute).Format(time.RFC3339)) }, ""},
		{"ts at now+5m+1s rejected", func(r *PositionRequest) {
			r.TS = str(testNow.Add(5*time.Minute + time.Second).Format(time.RFC3339))
		}, "ts"},
		{"ts at now-24h accepted", func(r *PositionRequest) { r.TS = str(testNow.Add(-24 * time.Hour).Format(time.RFC3339)) }, ""},
		{"ts at now-24h-1s rejected", func(r *PositionRequest) {
			r.TS = str(testNow.Add(-24*time.Hour - time.Second).Format(time.RFC3339))
		}, "ts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := validRequest()
			tc.mutate(&req)
			pos, err := Validate(req, testNow)
			if tc.field == "" {
				if err != nil {
					t.Fatalf("want accept, got %v", err)
				}
				return
			}
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Field != tc.field {
				t.Fatalf("want FieldError{%s}, got %v", tc.field, err)
			}
			if !errors.Is(err, ErrInvalidPosition) {
				t.Fatalf("error does not wrap ErrInvalidPosition: %v", err)
			}
			if pos != (Position{}) {
				t.Fatalf("rejected request returned a position: %+v", pos)
			}
		})
	}
}

func TestValidatePositionConvertsDeviceTSToUTC(t *testing.T) {
	req := validRequest()
	req.TS = str("2026-10-25T15:00:00+03:00")
	pos, err := Validate(req, testNow)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if want := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC); !pos.DeviceTS.Equal(want) || pos.DeviceTS.Location() != time.UTC {
		t.Fatalf("DeviceTS = %v, want %v UTC", pos.DeviceTS, want)
	}
}

// decode runs DecodePosition over a raw body the way the handler does.
func decode(t *testing.T, body string) (PositionRequest, error) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/ingest/position", strings.NewReader(body))
	return DecodePosition(httptest.NewRecorder(), r)
}

const validBody = `{"lat":41,"lng":29,"speed":50,"heading":90,"accuracy":8,"ts":"2026-10-25T12:00:00Z"}`

func TestValidatePositionBodyRules(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
	}{
		{"unknown field", `{"lat":41,"lng":29,"speed":50,"heading":90,"accuracy":8,"ts":"2026-10-25T12:00:00Z","extra":1}`, "extra"},
		{"string-typed number", `{"lat":"41","lng":29,"speed":50,"heading":90,"accuracy":8,"ts":"2026-10-25T12:00:00Z"}`, "lat"},
		{"string-typed speed", `{"lat":41,"lng":29,"speed":"50","heading":90,"accuracy":8,"ts":"2026-10-25T12:00:00Z"}`, "speed"},
		{"numeric ts", `{"lat":41,"lng":29,"speed":50,"heading":90,"accuracy":8,"ts":1798200000}`, "ts"},
		{"trailing JSON value", validBody + ` {}`, "body"},
		{"trailing garbage", validBody + `x`, "body"},
		{"empty body", ``, "body"},
		{"malformed JSON", `{"lat":`, "body"},
		{"body over 4096 bytes", `{"lat":41,"pad":"` + strings.Repeat("x", MaxBodyBytes) + `"}`, "body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decode(t, tc.body)
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Field != tc.field {
				t.Fatalf("want FieldError{%s}, got %v", tc.field, err)
			}
			if !errors.Is(err, ErrInvalidPosition) {
				t.Fatalf("error does not wrap ErrInvalidPosition: %v", err)
			}
		})
	}
}

func TestValidatePositionBodyCapIsInclusive(t *testing.T) {
	// Trailing whitespace is valid JSON, so it pads the body to the cap exactly.
	atCap := validBody + strings.Repeat(" ", MaxBodyBytes-len(validBody))
	if _, err := decode(t, atCap); err != nil {
		t.Fatalf("body of exactly %d bytes: %v", MaxBodyBytes, err)
	}
	overCap := atCap + " "
	if _, err := decode(t, overCap); !errors.Is(err, ErrInvalidPosition) {
		t.Fatalf("body of %d bytes: want ErrInvalidPosition, got %v", len(overCap), err)
	}
}
