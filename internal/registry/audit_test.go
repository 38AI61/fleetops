package registry

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// auditRows counts every admin_audit row.
func auditRows(t *testing.T, e *httpEnv) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM admin_audit`).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	return n
}

// expectAudit sends one request and checks its audit effect (GF-44). An empty
// action means the request must write no audit row. Otherwise exactly one row
// must appear, carrying the caller, its fleet, and the action. An empty target
// means the id is not known yet (a create), so only the kind prefix is checked.
func expectAudit(t *testing.T, e *httpEnv, c caller, method, path, body string, status int, action, target string) reply {
	t.Helper()
	before := auditRows(t, e)
	r := e.send(t, c, method, path, body)
	if r.status != status {
		t.Fatalf("%s %s = %d %s, want %d", method, path, r.status, r.body, status)
	}
	added := auditRows(t, e) - before
	if action == "" {
		if added != 0 {
			t.Fatalf("%s %s wrote %d audit rows, want none", method, path, added)
		}
		return r
	}
	if added != 1 {
		t.Fatalf("%s %s wrote %d audit rows, want 1", method, path, added)
	}
	var actor, fleet int64
	var gotAction, gotTarget string
	if err := e.db.QueryRow(
		`SELECT actor_id, fleet_id, action, target FROM admin_audit ORDER BY id DESC LIMIT 1`,
	).Scan(&actor, &fleet, &gotAction, &gotTarget); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if actor != c.id || fleet != c.fleet || gotAction != action {
		t.Fatalf("audit = actor %d fleet %d %s, want actor %d fleet %d %s",
			actor, fleet, gotAction, c.id, c.fleet, action)
	}
	if target == "" {
		kind := strings.SplitN(action, ".", 2)[0]
		if !strings.HasPrefix(gotTarget, kind+":") {
			t.Fatalf("audit target %q, want %s:<id>", gotTarget, kind)
		}
	} else if gotTarget != target {
		t.Fatalf("audit target %q, want %q", gotTarget, target)
	}
	return r
}

// checkLastTarget asserts the newest audit row names the given target exactly.
func checkLastTarget(t *testing.T, e *httpEnv, want string) {
	t.Helper()
	if got := lastAuditTarget(t, e); got != want {
		t.Fatalf("audit target = %q, want %q", got, want)
	}
}

// idFrom reads the id field of a JSON object body.
func idFrom(t *testing.T, r reply) int64 {
	t.Helper()
	var row struct {
		ID int64 `json:"id"`
	}
	decode(t, r.body, &row)
	if row.ID == 0 {
		t.Fatalf("no id in %s", r.body)
	}
	return row.ID
}

// TestRegistryAuditRecordsEachSuccessfulWrite walks every P3 write and checks
// the one audit row each produces, including writes by manager and dispatcher.
func TestRegistryAuditRecordsEachSuccessfulWrite(t *testing.T) {
	e := newHTTPEnv(t)
	fleet := insertFleet(t, e.db, "A")
	admin := e.as(t, fleet, roleAdmin)
	manager := e.as(t, fleet, roleManager)
	dispatcher := e.as(t, fleet, roleDispatcher)

	v := idFrom(t, expectAudit(t, e, admin, http.MethodPost, "/vehicles", `{"plate":"AUD0001"}`,
		http.StatusCreated, "vehicle.create", ""))
	vehicle := fmt.Sprintf("vehicle:%d", v)
	checkLastTarget(t, e, vehicle)
	expectAudit(t, e, manager, http.MethodPatch, fmt.Sprintf("/vehicles/%d", v), `{"model":"Caddy"}`,
		http.StatusOK, "vehicle.update", vehicle)
	v2 := idFrom(t, expectAudit(t, e, dispatcher, http.MethodPost, "/vehicles", `{"plate":"AUD0002"}`,
		http.StatusCreated, "vehicle.create", ""))
	checkLastTarget(t, e, fmt.Sprintf("vehicle:%d", v2))

	d := idFrom(t, expectAudit(t, e, admin, http.MethodPost, "/drivers", `{"name":"Dee","license_no":"DEE00001"}`,
		http.StatusCreated, "driver.create", ""))
	expectAudit(t, e, admin, http.MethodPatch, fmt.Sprintf("/drivers/%d", d), `{"phone":"+90 1"}`,
		http.StatusOK, "driver.update", fmt.Sprintf("driver:%d", d))

	devA := idFrom(t, expectAudit(t, e, admin, http.MethodPost, "/devices", `{"kind":"pwa"}`,
		http.StatusCreated, "device.issue", ""))
	devAName := fmt.Sprintf("device:%d", devA)
	expectAudit(t, e, admin, http.MethodPost, fmt.Sprintf("/devices/%d/rotate", devA), "",
		http.StatusOK, "device.rotate", devAName)
	devB := idFrom(t, expectAudit(t, e, admin, http.MethodPost, "/devices", `{"kind":"tracker"}`,
		http.StatusCreated, "device.issue", ""))

	pairPath := fmt.Sprintf("/vehicles/%d/pairings", v)
	expectAudit(t, e, admin, http.MethodPost, pairPath, fmt.Sprintf(`{"device_id":%d}`, devA),
		http.StatusCreated, "vehicle.pair", vehicle)
	expectAudit(t, e, manager, http.MethodPost, pairPath, fmt.Sprintf(`{"device_id":%d}`, devB),
		http.StatusCreated, "vehicle.pair", vehicle)
	expectAudit(t, e, admin, http.MethodPost, pairPath, `{"device_id":null}`,
		http.StatusOK, "vehicle.unpair", vehicle)
	t.Run("no-op unpair writes no audit row", func(t *testing.T) {
		expectAudit(t, e, admin, http.MethodPost, pairPath, `{"device_id":null}`, http.StatusOK, "", "")
	})
	expectAudit(t, e, admin, http.MethodPost, fmt.Sprintf("/devices/%d/revoke", devA), "",
		http.StatusOK, "device.revoke", devAName)
}

// lastAuditTarget returns the target of the newest audit row.
func lastAuditTarget(t *testing.T, e *httpEnv) string {
	t.Helper()
	var target string
	if err := e.db.QueryRow(`SELECT target FROM admin_audit ORDER BY id DESC LIMIT 1`).Scan(&target); err != nil {
		t.Fatalf("read audit target: %v", err)
	}
	return target
}

// TestRegistryAuditFailedWritesWriteNothing: every refused write (400, 403,
// 404, 409) leaves admin_audit untouched.
func TestRegistryAuditFailedWritesWriteNothing(t *testing.T) {
	e := newHTTPEnv(t)
	fleet := insertFleet(t, e.db, "A")
	other := insertFleet(t, e.db, "B")
	admin := e.as(t, fleet, roleAdmin)
	dispatcher := e.as(t, fleet, roleDispatcher)
	foreign := e.as(t, other, roleAdmin)

	v := idFrom(t, expectAudit(t, e, admin, http.MethodPost, "/vehicles", `{"plate":"FAIL01"}`,
		http.StatusCreated, "vehicle.create", ""))
	vPath := fmt.Sprintf("/vehicles/%d", v)
	dev := idFrom(t, expectAudit(t, e, admin, http.MethodPost, "/devices", `{"kind":"pwa"}`,
		http.StatusCreated, "device.issue", ""))
	revoked := idFrom(t, expectAudit(t, e, admin, http.MethodPost, "/devices", `{"kind":"pwa"}`,
		http.StatusCreated, "device.issue", ""))
	expectAudit(t, e, admin, http.MethodPost, fmt.Sprintf("/devices/%d/revoke", revoked), "",
		http.StatusOK, "device.revoke", fmt.Sprintf("device:%d", revoked))
	other2 := idFrom(t, expectAudit(t, e, admin, http.MethodPost, "/vehicles", `{"plate":"FAIL02"}`,
		http.StatusCreated, "vehicle.create", ""))
	expectAudit(t, e, admin, http.MethodPost, fmt.Sprintf("/vehicles/%d/pairings", other2),
		fmt.Sprintf(`{"device_id":%d}`, dev), http.StatusCreated, "vehicle.pair", fmt.Sprintf("vehicle:%d", other2))
	expectAudit(t, e, admin, http.MethodPatch, fmt.Sprintf("/vehicles/%d", other2), `{"status":"retired"}`,
		http.StatusOK, "vehicle.update", fmt.Sprintf("vehicle:%d", other2))

	cases := []struct {
		name   string
		c      caller
		method string
		path   string
		body   string
		status int
	}{
		{"400 bad plate", admin, http.MethodPost, "/vehicles", `{"plate":"x"}`, http.StatusBadRequest},
		{"400 unknown field", admin, http.MethodPost, "/drivers", `{"name":"x","license_no":"ABCD1234","role":"admin"}`, http.StatusBadRequest},
		{"403 dispatcher patches a vehicle", dispatcher, http.MethodPatch, vPath, `{"model":"x"}`, http.StatusForbidden},
		{"403 dispatcher pairs", dispatcher, http.MethodPost, vPath + "/pairings", fmt.Sprintf(`{"device_id":%d}`, dev), http.StatusForbidden},
		{"404 cross-fleet patch", foreign, http.MethodPatch, vPath, `{"model":"x"}`, http.StatusNotFound},
		{"409 duplicate plate", admin, http.MethodPost, "/vehicles", `{"plate":"fail01"}`, http.StatusConflict},
		{"409 rotate revoked device", admin, http.MethodPost, fmt.Sprintf("/devices/%d/rotate", revoked), "", http.StatusConflict},
		{"409 pair revoked device", admin, http.MethodPost, vPath + "/pairings", fmt.Sprintf(`{"device_id":%d}`, revoked), http.StatusConflict},
		{"409 retired vehicle", admin, http.MethodPost, fmt.Sprintf("/vehicles/%d/pairings", other2), fmt.Sprintf(`{"device_id":%d}`, dev), http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectAudit(t, e, tc.c, tc.method, tc.path, tc.body, tc.status, "", "")
		})
	}
}

// TestRegistryAuditHasNoSecrets: admin_audit has no payload column, and no
// audit row carries a key, a license number, or a secret (GF-44).
func TestRegistryAuditHasNoSecrets(t *testing.T) {
	e := newHTTPEnv(t)
	fleet := insertFleet(t, e.db, "A")
	admin := e.as(t, fleet, roleAdmin)

	var cols int
	if err := e.db.QueryRow(
		`SELECT count(*) FROM information_schema.columns WHERE table_name = 'admin_audit'`,
	).Scan(&cols); err != nil || cols != 6 {
		t.Fatalf("admin_audit has %d columns (%v), want the six from migration 003", cols, err)
	}

	const license = "SECRETLIC42"
	expectAudit(t, e, admin, http.MethodPost, "/drivers", fmt.Sprintf(`{"name":"S","license_no":"%s"}`, license),
		http.StatusCreated, "driver.create", "")
	issued := e.send(t, admin, http.MethodPost, "/devices", `{"kind":"pwa"}`)
	if issued.status != http.StatusCreated {
		t.Fatalf("issue = %d %s", issued.status, issued.body)
	}
	var dev IssuedDevice
	decode(t, issued.body, &dev)
	rotated := e.send(t, admin, http.MethodPost, fmt.Sprintf("/devices/%d/rotate", dev.ID), "")
	if rotated.status != http.StatusOK {
		t.Fatalf("rotate = %d %s", rotated.status, rotated.body)
	}

	var all string
	if err := e.db.QueryRow(
		`SELECT coalesce(string_agg(action || ' ' || target, ' | '), '') FROM admin_audit`,
	).Scan(&all); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if all == "" {
		t.Fatal("no audit rows were written")
	}
	for _, leak := range []string{license, "fo_dev_", dev.APIKey} {
		if strings.Contains(all, leak) {
			t.Fatalf("audit rows contain %q: %s", leak, all)
		}
	}
}
