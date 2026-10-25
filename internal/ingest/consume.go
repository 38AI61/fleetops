package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
)

// ErrMalformedPayload is returned for any GF-58 payload the consumer cannot
// decode. The consumer terminates such a message on first delivery (GF-89).
var ErrMalformedPayload = errors.New("ingest: malformed payload")

// wireIn mirrors wirePayload with pointers, so a missing or null key is
// detected (GF-96). Unknown keys are rejected by the decoder.
type wireIn struct {
	MsgID      *string  `json:"msg_id"`
	FleetID    *int64   `json:"fleet_id"`
	DeviceID   *int64   `json:"device_id"`
	Lat        *float64 `json:"lat"`
	Lng        *float64 `json:"lng"`
	SpeedKmh   *float64 `json:"speed_kmh"`
	HeadingDeg *float64 `json:"heading_deg"`
	AccuracyM  *float64 `json:"accuracy_m"`
	DeviceTS   *string  `json:"device_ts"`
	ServerTS   *string  `json:"server_ts"`
}

// ParseMessage is the inverse of Message.Payload (GF-96). It rejects unknown
// keys, missing keys, trailing data, a bad UUID, non-positive ids, and bad
// timestamps. The subject is not parsed: the fleet id comes from the payload.
func ParseMessage(data []byte) (Message, error) {
	var w wireIn
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return Message{}, fmt.Errorf("%w: %w", ErrMalformedPayload, err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Message{}, fmt.Errorf("%w: trailing data", ErrMalformedPayload)
	}

	for _, present := range []bool{
		w.MsgID != nil, w.FleetID != nil, w.DeviceID != nil,
		w.Lat != nil, w.Lng != nil, w.SpeedKmh != nil,
		w.HeadingDeg != nil, w.AccuracyM != nil,
		w.DeviceTS != nil, w.ServerTS != nil,
	} {
		if !present {
			return Message{}, fmt.Errorf("%w: missing key", ErrMalformedPayload)
		}
	}

	id, err := uuid.Parse(*w.MsgID)
	if err != nil {
		return Message{}, fmt.Errorf("%w: msg_id", ErrMalformedPayload)
	}
	if *w.FleetID <= 0 {
		return Message{}, fmt.Errorf("%w: fleet_id", ErrMalformedPayload)
	}
	if *w.DeviceID <= 0 {
		return Message{}, fmt.Errorf("%w: device_id", ErrMalformedPayload)
	}
	deviceTS, err := time.Parse(time.RFC3339Nano, *w.DeviceTS)
	if err != nil {
		return Message{}, fmt.Errorf("%w: device_ts", ErrMalformedPayload)
	}
	serverTS, err := time.Parse(time.RFC3339Nano, *w.ServerTS)
	if err != nil {
		return Message{}, fmt.Errorf("%w: server_ts", ErrMalformedPayload)
	}

	return Message{
		MsgID:    id,
		FleetID:  *w.FleetID,
		DeviceID: *w.DeviceID,
		Position: Position{
			Lat:      *w.Lat,
			Lng:      *w.Lng,
			Speed:    *w.SpeedKmh,
			Heading:  *w.HeadingDeg,
			Accuracy: *w.AccuracyM,
			DeviceTS: deviceTS.UTC(),
		},
		ServerTS: serverTS.UTC(),
	}, nil
}

// NAK schedule (GF-88): 1 s doubling, capped at 8 s.
const (
	nakBase = time.Second
	nakCap  = 8 * time.Second
)

// nakDelay is min(1 s × 2^(numDelivered−1), 8 s). The first delivery waits 1 s.
func nakDelay(numDelivered int) time.Duration {
	d := nakBase
	for i := 1; i < numDelivered && d < nakCap; i++ {
		d *= 2
	}
	return min(d, nakCap)
}

// shouldTerm reports whether a failed message has used its last attempt (GF-89).
func shouldTerm(numDelivered, maxDeliver int) bool {
	return numDelivered >= maxDeliver
}
