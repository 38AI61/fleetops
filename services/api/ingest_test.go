package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"fleetops/internal/devicekey"
	"fleetops/internal/ingest"
	"fleetops/internal/registry"
	"fleetops/internal/testinfra"
)

// ingestFixture is a fleet, an admin who issues devices, and one issued device.
type ingestFixture struct {
	fleet int64
	admin registry.Actor
	dev   registry.IssuedDevice
	svc   *registry.Service
}

func newIngestFixture(t *testing.T, h *harness) ingestFixture {
	t.Helper()
	fleet := h.fleet(t, "Ingest")
	adminID := h.user(t, fleet, "admin@ingest.example", "admin")
	svc := &registry.Service{DB: h.db, Salt: testSalt, Clock: h.clock.now}
	actor := registry.Actor{UserID: adminID, FleetID: fleet}
	dev, err := svc.IssueDevice(context.Background(), actor, registry.KindPWA)
	if err != nil {
		t.Fatalf("issue device: %v", err)
	}
	return ingestFixture{fleet: fleet, admin: actor, dev: dev, svc: svc}
}

// positionJSON is a valid body with the given device timestamp.
func positionJSON(ts string) string {
	return fmt.Sprintf(`{"lat":41.0082,"lng":28.9784,"speed":62.5,"heading":181,"accuracy":7.25,"ts":%q}`, ts)
}

// validBodyNow is a valid body stamped at the harness clock.
func validBodyNow(h *harness) string {
	return positionJSON(h.clock.t.Format(time.RFC3339))
}

// postPosition sends one POST /ingest/position with the given X-Device-Key value.
// An empty key still sends the header, so the handler sees an empty value.
func postPosition(t *testing.T, h *harness, key, body string) reply {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.url+"/ingest/position", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /ingest/position: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{status: resp.StatusCode, body: b}
}

// streamMsgs returns the message count of GPS_RAW.
func streamMsgs(t *testing.T, h *harness) uint64 {
	t.Helper()
	ctx := context.Background()
	s, err := h.js.Stream(ctx, ingest.StreamName)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	return info.State.Msgs
}

func TestIngestPositionValid204(t *testing.T) {
	h := startServer(t)
	f := newIngestFixture(t, h)

	before := h.clock.t
	r := postPosition(t, h, f.dev.APIKey, validBodyNow(h))
	if r.status != http.StatusNoContent {
		t.Fatalf("status = %d (%s), want 204", r.status, r.body)
	}

	subject := fmt.Sprintf("gps.raw.%d", f.fleet)
	got := ingestFetch(t, h, subject)

	id, err := uuid.Parse(got["msg_id"].(string))
	if err != nil {
		t.Fatalf("msg_id: %v", err)
	}
	if id.Version() != 7 {
		t.Fatalf("msg_id version = %d, want 7", id.Version())
	}
	serverTS, err := time.Parse(time.RFC3339Nano, got["server_ts"].(string))
	if err != nil {
		t.Fatalf("server_ts: %v", err)
	}
	if !serverTS.Equal(before) {
		t.Fatalf("server_ts = %v, want the test clock %v", serverTS, before)
	}
	if got["device_id"] != float64(f.dev.ID) || got["fleet_id"] != float64(f.fleet) {
		t.Fatalf("device_id/fleet_id = %v/%v, want %d/%d", got["device_id"], got["fleet_id"], f.dev.ID, f.fleet)
	}
	for k, want := range map[string]float64{"lat": 41.0082, "lng": 28.9784, "speed_kmh": 62.5, "heading_deg": 181, "accuracy_m": 7.25} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v", k, got[k], want)
		}
	}
	if got["device_ts"] != before.UTC().Format(time.RFC3339Nano) {
		t.Errorf("device_ts = %v", got["device_ts"])
	}
}

// ingestFetch returns the one payload on subject, decoded.
func ingestFetch(t *testing.T, h *harness, subject string) map[string]any {
	t.Helper()
	msgs := testinfra.FetchSubject(t, h.js, ingest.StreamName, subject, 1, 5*time.Second)
	var m map[string]any
	if err := json.Unmarshal(msgs[0], &m); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return m
}

func TestIngestPositionInvalidPublishesNothing(t *testing.T) {
	h := startServer(t)
	f := newIngestFixture(t, h)
	now := h.clock.t

	cases := []struct {
		name string
		body string
	}{
		{"lat out of range", strings.Replace(validBodyNow(h), `"lat":41.0082`, `"lat":91`, 1)},
		{"missing ts", `{"lat":41,"lng":29,"speed":50,"heading":90,"accuracy":8}`},
		{"unknown field", strings.Replace(validBodyNow(h), `"ts"`, `"extra":1,"ts"`, 1)},
		{"string-typed speed", strings.Replace(validBodyNow(h), `"speed":62.5`, `"speed":"62.5"`, 1)},
		{"body over 4096 bytes", validBodyNow(h) + strings.Repeat(" ", ingest.MaxBodyBytes)},
		{"ts 25 h old", positionJSON(now.Add(-25 * time.Hour).Format(time.RFC3339))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := postPosition(t, h, f.dev.APIKey, tc.body)
			if r.status != http.StatusBadRequest || r.errCode(t) != "validation_failed" {
				t.Fatalf("status = %d %s, want 400 validation_failed", r.status, r.body)
			}
		})
	}

	if n := streamMsgs(t, h); n != 0 {
		t.Fatalf("stream holds %d messages after rejects, want 0", n)
	}
}

func TestIngestPositionRevokedKey401(t *testing.T) {
	h := startServer(t)
	f := newIngestFixture(t, h)

	// A valid key works before revoke, so the 401s below are about revocation, not a bad key.
	if r := postPosition(t, h, f.dev.APIKey, validBodyNow(h)); r.status != http.StatusNoContent {
		t.Fatalf("pre-revoke status = %d (%s), want 204", r.status, r.body)
	}
	if _, err := f.svc.RevokeDevice(context.Background(), f.admin, f.dev.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	published := streamMsgs(t, h)

	// A well-formed key for the same device id, with a secret that was never stored.
	secret, _, err := devicekey.GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	wrong := devicekey.Format(f.dev.ID, secret)

	cases := []struct {
		name string
		key  string
	}{
		{"revoked device, previously valid key", f.dev.APIKey},
		{"wrong secret for a real device id", wrong},
		{"malformed key", "not-a-device-key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := postPosition(t, h, tc.key, validBodyNow(h))
			if r.status != http.StatusUnauthorized || r.errCode(t) != "unauthorized" {
				t.Fatalf("status = %d %s, want 401 unauthorized", r.status, r.body)
			}
		})
	}

	if n := streamMsgs(t, h); n != published {
		t.Fatalf("stream grew from %d to %d after refused keys, want no new messages", published, n)
	}
}

func TestIngestPositionFullBuffer503(t *testing.T) {
	h := startServer(t)
	f := newIngestFixture(t, h)

	release := h.gate.HoldSlots(ingest.MaxInFlight)
	r := postPosition(t, h, f.dev.APIKey, validBodyNow(h))
	if r.status != http.StatusServiceUnavailable || r.errCode(t) != "busy" {
		t.Fatalf("full gate: status = %d %s, want 503 busy", r.status, r.body)
	}
	if n := streamMsgs(t, h); n != 0 {
		t.Fatalf("busy request published %d messages, want 0", n)
	}

	release()
	if r := postPosition(t, h, f.dev.APIKey, validBodyNow(h)); r.status != http.StatusNoContent {
		t.Fatalf("after release: status = %d (%s), want 204", r.status, r.body)
	}
}
