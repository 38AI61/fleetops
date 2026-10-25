// Package ingest accepts device position reports and publishes them to the
// GPS_RAW stream (README §3, D-15, D-16). P4 stops at the stream: no buffer,
// no database writes, no consumer.
package ingest

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// MaxBodyBytes caps a position body (GF-54). A position is under 200 bytes.
const MaxBodyBytes = 4096

// Bounds and the timestamp window (GF-55, GF-56).
const (
	minLat, maxLat           = -90.0, 90.0
	minLng, maxLng           = -180.0, 180.0
	minSpeed, maxSpeed       = 0.0, 300.0
	minHeading, maxHeading   = 0.0, 360.0
	minAccuracy, maxAccuracy = 0.0, 10000.0

	tsPastWindow   = 24 * time.Hour
	tsFutureWindow = 5 * time.Minute
)

// ErrInvalidPosition is the sentinel every validation failure wraps.
var ErrInvalidPosition = errors.New("ingest: invalid position")

// FieldError names the field that failed. It never carries the value.
type FieldError struct {
	Field string
}

func (e *FieldError) Error() string {
	return "ingest: invalid field " + e.Field
}

// Unwrap lets errors.Is(err, ErrInvalidPosition) match every FieldError.
func (e *FieldError) Unwrap() error {
	return ErrInvalidPosition
}

// PositionRequest is the wire body. Pointers detect a missing or null field
// (GF-55); a string-typed number fails decoding rather than coercing.
type PositionRequest struct {
	Lat      *float64 `json:"lat"`
	Lng      *float64 `json:"lng"`
	Speed    *float64 `json:"speed"`
	Heading  *float64 `json:"heading"`
	Accuracy *float64 `json:"accuracy"`
	TS       *string  `json:"ts"`
}

// Position is a validated report. DeviceTS is the device clock in UTC.
type Position struct {
	Lat      float64
	Lng      float64
	Speed    float64
	Heading  float64
	Accuracy float64
	DeviceTS time.Time
}

// DecodePosition reads exactly one strict JSON value from the request body,
// capped at MaxBodyBytes (GF-54). Content-Type is not checked. Every decode
// failure returns a *FieldError: the offending field when the decoder names
// one, otherwise "body".
func DecodePosition(w http.ResponseWriter, r *http.Request) (PositionRequest, error) {
	var req PositionRequest
	body := http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return PositionRequest{}, decodeFieldError(err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return PositionRequest{}, &FieldError{Field: "body"}
	}
	return req, nil
}

func decodeFieldError(err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		return &FieldError{Field: typeErr.Field}
	}
	if name, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		return &FieldError{Field: strings.Trim(name, `"`)}
	}
	return &FieldError{Field: "body"}
}

// Validate checks every field against GF-55 and the ts window in GF-56.
// Required fields are checked first, in struct order, then bounds.
func Validate(req PositionRequest, now time.Time) (Position, error) {
	lat, err := requireNumber("lat", req.Lat, minLat, maxLat)
	if err != nil {
		return Position{}, err
	}
	lng, err := requireNumber("lng", req.Lng, minLng, maxLng)
	if err != nil {
		return Position{}, err
	}
	speed, err := requireNumber("speed", req.Speed, minSpeed, maxSpeed)
	if err != nil {
		return Position{}, err
	}
	heading, err := requireNumber("heading", req.Heading, minHeading, maxHeading)
	if err != nil {
		return Position{}, err
	}
	accuracy, err := requireNumber("accuracy", req.Accuracy, minAccuracy, maxAccuracy)
	if err != nil {
		return Position{}, err
	}
	if req.TS == nil {
		return Position{}, &FieldError{Field: "ts"}
	}
	ts, err := time.Parse(time.RFC3339, *req.TS)
	if err != nil {
		return Position{}, &FieldError{Field: "ts"}
	}
	if ts.Before(now.Add(-tsPastWindow)) || ts.After(now.Add(tsFutureWindow)) {
		return Position{}, &FieldError{Field: "ts"}
	}
	return Position{
		Lat:      lat,
		Lng:      lng,
		Speed:    speed,
		Heading:  heading,
		Accuracy: accuracy,
		DeviceTS: ts.UTC(),
	}, nil
}

func requireNumber(field string, v *float64, lo, hi float64) (float64, error) {
	if v == nil || *v < lo || *v > hi {
		return 0, &FieldError{Field: field}
	}
	return *v, nil
}
