package ingest

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"pgregory.net/rapid"
)

// genMessage draws a Message whose fields survive Payload and ParseMessage
// unchanged: finite floats, UTC timestamps at nanosecond precision.
func genMessage(rt *rapid.T) Message {
	var id uuid.UUID
	copy(id[:], rapid.SliceOfN(rapid.Byte(), 16, 16).Draw(rt, "msg_id"))
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return Message{
		MsgID:    id,
		FleetID:  rapid.Int64Range(1, 1<<40).Draw(rt, "fleet_id"),
		DeviceID: rapid.Int64Range(1, 1<<40).Draw(rt, "device_id"),
		Position: Position{
			Lat:      rapid.Float64Range(-90, 90).Draw(rt, "lat"),
			Lng:      rapid.Float64Range(-180, 180).Draw(rt, "lng"),
			Speed:    rapid.Float64Range(0, 300).Draw(rt, "speed"),
			Heading:  rapid.Float64Range(0, 360).Draw(rt, "heading"),
			Accuracy: rapid.Float64Range(0, 10000).Draw(rt, "accuracy"),
			DeviceTS: base.Add(time.Duration(rapid.Int64Range(0, 1<<50).Draw(rt, "device_ns"))).UTC(),
		},
		ServerTS: base.Add(time.Duration(rapid.Int64Range(0, 1<<50).Draw(rt, "server_ns"))).UTC(),
	}
}

func TestParseMessageRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		want := genMessage(rt)
		payload, err := want.Payload()
		if err != nil {
			rt.Fatalf("Payload: %v", err)
		}
		got, err := ParseMessage(payload)
		if err != nil {
			rt.Fatalf("ParseMessage: %v", err)
		}
		if got.MsgID != want.MsgID || got.FleetID != want.FleetID || got.DeviceID != want.DeviceID {
			rt.Fatalf("ids: got %+v, want %+v", got, want)
		}
		if got.Position.Lat != want.Position.Lat || got.Position.Lng != want.Position.Lng ||
			got.Position.Speed != want.Position.Speed || got.Position.Heading != want.Position.Heading ||
			got.Position.Accuracy != want.Position.Accuracy {
			rt.Fatalf("position: got %+v, want %+v", got.Position, want.Position)
		}
		if !got.Position.DeviceTS.Equal(want.Position.DeviceTS) || !got.ServerTS.Equal(want.ServerTS) {
			rt.Fatalf("timestamps: got %v/%v, want %v/%v",
				got.Position.DeviceTS, got.ServerTS, want.Position.DeviceTS, want.ServerTS)
		}
	})
}

// validPayload is a complete GF-58 body. Each malformed case edits one part of it.
const validPayload = `{"msg_id":"0190a8c4-1234-7abc-8def-0123456789ab","fleet_id":7,"device_id":9,` +
	`"lat":41,"lng":29,"speed_kmh":50,"heading_deg":90,"accuracy_m":8,` +
	`"device_ts":"2026-10-25T12:00:00Z","server_ts":"2026-10-25T12:00:00.5Z"}`

func TestParseMessageValidFixture(t *testing.T) {
	m, err := ParseMessage([]byte(validPayload))
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	if m.FleetID != 7 || m.DeviceID != 9 {
		t.Fatalf("ids = %d/%d, want 7/9", m.FleetID, m.DeviceID)
	}
}

func TestParseMessageMalformed(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{"bad json", `{"msg_id":`},
		{"empty", ``},
		{"not an object", `"hello"`},
		{"null", `null`},
		{"unknown key", strings.Replace(validPayload, `"lat":41,`, `"lat":41,"extra":1,`, 1)},
		{"missing key", strings.Replace(validPayload, `"accuracy_m":8,`, ``, 1)},
		{"null key", strings.Replace(validPayload, `"lng":29`, `"lng":null`, 1)},
		{"string number", strings.Replace(validPayload, `"lng":29`, `"lng":"29"`, 1)},
		{"bad uuid", strings.Replace(validPayload, `0190a8c4-1234-7abc-8def-0123456789ab`, `not-a-uuid`, 1)},
		{"zero fleet id", strings.Replace(validPayload, `"fleet_id":7`, `"fleet_id":0`, 1)},
		{"negative fleet id", strings.Replace(validPayload, `"fleet_id":7`, `"fleet_id":-3`, 1)},
		{"fractional fleet id", strings.Replace(validPayload, `"fleet_id":7`, `"fleet_id":7.5`, 1)},
		{"zero device id", strings.Replace(validPayload, `"device_id":9`, `"device_id":0`, 1)},
		{"bad device_ts", strings.Replace(validPayload, `"2026-10-25T12:00:00Z"`, `"yesterday"`, 1)},
		{"trailing data", validPayload + `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseMessage([]byte(tc.payload))
			if !errors.Is(err, ErrMalformedPayload) {
				t.Fatalf("ParseMessage error = %v, want ErrMalformedPayload", err)
			}
		})
	}
}

func TestNakDelaySchedule(t *testing.T) {
	want := []time.Duration{1, 2, 4, 8, 8, 8, 8, 8}
	for i, w := range want {
		n := i + 1
		if got := nakDelay(n); got != w*time.Second {
			t.Fatalf("nakDelay(%d) = %s, want %s", n, got, w*time.Second)
		}
	}
	if got := nakDelay(0); got != time.Second {
		t.Fatalf("nakDelay(0) = %s, want 1s", got)
	}
	if got := nakDelay(1 << 30); got != 8*time.Second {
		t.Fatalf("nakDelay(huge) = %s, want 8s", got)
	}
}

func TestShouldTermAtMaxDeliver(t *testing.T) {
	const maxDeliver = 8
	for n := 1; n < maxDeliver; n++ {
		if shouldTerm(n, maxDeliver) {
			t.Fatalf("shouldTerm(%d, %d) = true before the last attempt", n, maxDeliver)
		}
	}
	if !shouldTerm(maxDeliver, maxDeliver) {
		t.Fatalf("shouldTerm(%d, %d) = false at the last attempt", maxDeliver, maxDeliver)
	}
	if !shouldTerm(maxDeliver+1, maxDeliver) {
		t.Fatal("shouldTerm past the last attempt = false")
	}
}
