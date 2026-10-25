package ingest

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"fleetops/internal/registry"
)

func fixedClock(t time.Time) Clock { return func() time.Time { return t } }

func testPosition() Position {
	return Position{
		Lat: 41.0082, Lng: 28.9784, Speed: 62.5, Heading: 181, Accuracy: 7.25,
		DeviceTS: time.Date(2026, 10, 25, 11, 59, 58, 500000000, time.UTC),
	}
}

func TestStampMessageIDIsUUIDv7(t *testing.T) {
	dev := registry.Device{ID: 7, FleetID: 42}
	clock := fixedClock(testNow)
	a, err := Stamp(dev, testPosition(), clock, uuid.NewV7)
	if err != nil {
		t.Fatalf("Stamp: %v", err)
	}
	b, err := Stamp(dev, testPosition(), clock, uuid.NewV7)
	if err != nil {
		t.Fatalf("Stamp: %v", err)
	}
	for _, m := range []Message{a, b} {
		if v := m.MsgID.Version(); v != 7 {
			t.Fatalf("msg_id version = %d, want 7", v)
		}
		if vr := m.MsgID.Variant(); vr != uuid.RFC4122 {
			t.Fatalf("msg_id variant = %v, want RFC 4122", vr)
		}
	}
	if a.MsgID == b.MsgID {
		t.Fatalf("two stamps share msg_id %s", a.MsgID)
	}
}

func TestStampServerTSFromClock(t *testing.T) {
	// A non-UTC clock reading must come out as the same instant in UTC.
	loc := time.FixedZone("UTC+3", 3*60*60)
	instant := time.Date(2026, 10, 25, 15, 0, 0, 0, loc)
	m, err := Stamp(registry.Device{ID: 1, FleetID: 1}, testPosition(), fixedClock(instant), uuid.NewV7)
	if err != nil {
		t.Fatalf("Stamp: %v", err)
	}
	if !m.ServerTS.Equal(instant) {
		t.Fatalf("server_ts = %v, want %v", m.ServerTS, instant)
	}
	if m.ServerTS.Location() != time.UTC {
		t.Fatalf("server_ts location = %v, want UTC", m.ServerTS.Location())
	}
}

func TestStampSubjectUsesDeviceFleet(t *testing.T) {
	m, err := Stamp(registry.Device{ID: 9, FleetID: 42}, testPosition(), fixedClock(testNow), uuid.NewV7)
	if err != nil {
		t.Fatalf("Stamp: %v", err)
	}
	if got := m.Subject(); got != "gps.raw.42" {
		t.Fatalf("Subject = %q, want gps.raw.42", got)
	}
}

func TestStampPayloadKeys(t *testing.T) {
	m, err := Stamp(registry.Device{ID: 7, FleetID: 42}, testPosition(), fixedClock(testNow), uuid.NewV7)
	if err != nil {
		t.Fatalf("Stamp: %v", err)
	}
	raw, err := m.Payload()
	if err != nil {
		t.Fatalf("Payload: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"accuracy_m", "device_id", "device_ts", "fleet_id", "heading_deg", "lat", "lng", "msg_id", "server_ts", "speed_kmh"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("payload keys = %v, want %v", keys, want)
	}
	if got["msg_id"] != m.MsgID.String() {
		t.Errorf("msg_id = %v, want %s", got["msg_id"], m.MsgID)
	}
	if got["fleet_id"] != float64(42) || got["device_id"] != float64(7) {
		t.Errorf("fleet_id/device_id = %v/%v", got["fleet_id"], got["device_id"])
	}
	if got["speed_kmh"] != 62.5 || got["heading_deg"] != float64(181) || got["accuracy_m"] != 7.25 {
		t.Errorf("speed/heading/accuracy = %v/%v/%v", got["speed_kmh"], got["heading_deg"], got["accuracy_m"])
	}
	if got["device_ts"] != "2026-10-25T11:59:58.5Z" {
		t.Errorf("device_ts = %v, want RFC3339Nano UTC", got["device_ts"])
	}
	if got["server_ts"] != "2026-10-25T12:00:00Z" {
		t.Errorf("server_ts = %v, want RFC3339Nano UTC", got["server_ts"])
	}
}
