package registry

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"fleetops/internal/devicekey"
	"fleetops/internal/testinfra"
)

// storeSalt is 32 bytes, used only by tests.
var storeSalt = []byte("store-test-salt-0123456789abcdef")

// fixture is one migrated database with two fleets and an admin in each.
type fixture struct {
	db     *sql.DB
	svc    *Service
	clock  *time.Time
	fleetA int64
	fleetB int64
	actorA Actor
	actorB Actor
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := testinfra.NewMigratedDB(t)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	f := &fixture{db: db, clock: &now}
	f.svc = &Service{DB: db, Salt: storeSalt, Clock: func() time.Time { return *f.clock }}
	f.fleetA = insertFleet(t, db, "A")
	f.fleetB = insertFleet(t, db, "B")
	f.actorA = Actor{UserID: insertUser(t, db, f.fleetA, "admin@a.example"), FleetID: f.fleetA}
	f.actorB = Actor{UserID: insertUser(t, db, f.fleetB, "admin@b.example"), FleetID: f.fleetB}
	return f
}

// tick advances the injected clock by one second.
func (f *fixture) tick() { *f.clock = f.clock.Add(time.Second) }

func insertFleet(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`INSERT INTO fleets (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert fleet: %v", err)
	}
	return id
}

func insertUser(t *testing.T, db *sql.DB, fleetID int64, email string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(
		`INSERT INTO users (fleet_id, email, password_hash, role) VALUES ($1, $2, 'x', 'admin') RETURNING id`,
		fleetID, email).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

func mustOK(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func TestVehicleStoreIsFleetScoped(t *testing.T) {
	f := newFixture(t)
	v, err := f.svc.CreateVehicle(context.Background(), f.actorA, VehicleInput{Plate: " ab-123 cd ", Model: "Transit"})
	mustOK(t, err, "create vehicle")
	if v.Plate != "AB-123 CD" || v.Status != VehicleActive || v.Model == nil || *v.Model != "Transit" {
		t.Fatalf("created vehicle = %+v", v)
	}

	list, err := f.svc.ListVehicles(context.Background(), f.fleetA)
	mustOK(t, err, "list A")
	if len(list) != 1 || list[0].ID != v.ID {
		t.Fatalf("list A = %+v", list)
	}
	if list, _ := f.svc.ListVehicles(context.Background(), f.fleetB); len(list) != 0 {
		t.Fatalf("list B leaks fleet A rows: %+v", list)
	}
	if _, err := f.svc.GetVehicle(context.Background(), f.fleetB, v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get across fleets = %v, want ErrNotFound", err)
	}

	status := VehicleMaintenance
	model := ""
	upd, err := f.svc.UpdateVehicle(context.Background(), f.actorA, v.ID, VehiclePatch{Status: &status, Model: &model})
	mustOK(t, err, "update vehicle")
	if upd.Status != VehicleMaintenance || upd.Model != nil {
		t.Fatalf("updated vehicle = %+v", upd)
	}
	if _, err := f.svc.UpdateVehicle(context.Background(), f.actorB, v.ID, VehiclePatch{Status: &status}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update across fleets = %v, want ErrNotFound", err)
	}
}

func TestVehicleStatusCheckRejectsUnknownStatus(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.CreateVehicle(context.Background(), f.actorA, VehicleInput{Plate: "AB12", Status: "broken"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("service create with status broken = %v, want ErrValidation", err)
	}
	_, err := f.db.Exec(`INSERT INTO vehicles (fleet_id, plate, status) VALUES ($1, 'ZZ99', 'broken')`, f.fleetA)
	if err == nil || !strings.Contains(err.Error(), "vehicles_status_check") {
		t.Fatalf("raw insert with status broken = %v, want vehicles_status_check violation", err)
	}
}

func TestVehiclePlateDuplicateInFleetIsConflict(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, err := f.svc.CreateVehicle(ctx, f.actorA, VehicleInput{Plate: "AB12"})
	mustOK(t, err, "first plate")
	if _, err := f.svc.CreateVehicle(ctx, f.actorA, VehicleInput{Plate: "ab12"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate plate in one fleet = %v, want ErrConflict", err)
	}
	if _, err := f.svc.CreateVehicle(ctx, f.actorB, VehicleInput{Plate: "AB12"}); err != nil {
		t.Fatalf("same plate in another fleet: %v", err)
	}
}

func TestDriverStoreIsFleetScopedAndRehashesLicense(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d, err := f.svc.CreateDriver(ctx, f.actorA, DriverInput{Name: "Ana", LicenseNo: "ab-12 cd34", ShiftStart: "08:00", ShiftEnd: "16:30"})
	mustOK(t, err, "create driver")
	if d.ShiftStart == nil || *d.ShiftStart != "08:00" || d.ShiftEnd == nil || *d.ShiftEnd != "16:30" {
		t.Fatalf("created driver shifts = %+v", d)
	}

	var before []byte
	mustOK(t, f.db.QueryRow(`SELECT license_no_hash FROM drivers WHERE id = $1`, d.ID).Scan(&before), "read hash")
	if !bytes.Equal(before, LicenseHash(storeSalt, f.fleetA, "AB12CD34")) {
		t.Fatal("stored license hash is not the D-31 hash of the normalized number")
	}

	newLicense := "ZZ99 00 11"
	name := "Ana B"
	upd, err := f.svc.UpdateDriver(ctx, f.actorA, d.ID, DriverPatch{Name: &name, LicenseNo: &newLicense})
	mustOK(t, err, "update driver")
	if upd.Name != "Ana B" {
		t.Fatalf("updated name = %q", upd.Name)
	}
	var after []byte
	mustOK(t, f.db.QueryRow(`SELECT license_no_hash FROM drivers WHERE id = $1`, d.ID).Scan(&after), "read hash")
	if bytes.Equal(before, after) || !bytes.Equal(after, LicenseHash(storeSalt, f.fleetA, "ZZ990011")) {
		t.Fatal("license change did not re-hash the stored value")
	}

	if list, _ := f.svc.ListDrivers(ctx, f.fleetB, nil); len(list) != 0 {
		t.Fatalf("fleet B lists fleet A drivers: %+v", list)
	}
	if _, err := f.svc.GetDriver(ctx, f.fleetB, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get driver across fleets = %v, want ErrNotFound", err)
	}
	if _, err := f.svc.UpdateDriver(ctx, f.actorB, d.ID, DriverPatch{Name: &name}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update driver across fleets = %v, want ErrNotFound", err)
	}
}

func TestDriverLicenseIsFleetScoped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	in := DriverInput{Name: "Same", LicenseNo: "AB12CD34"}
	_, err := f.svc.CreateDriver(ctx, f.actorA, in)
	mustOK(t, err, "fleet A first")
	if _, err := f.svc.CreateDriver(ctx, f.actorA, in); !errors.Is(err, ErrConflict) {
		t.Fatalf("same license twice in fleet A = %v, want ErrConflict", err)
	}
	_, err = f.svc.CreateDriver(ctx, f.actorB, in)
	mustOK(t, err, "same license in fleet B")
}

func TestDeviceIssueStoresOnlyHash(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	issued, err := f.svc.IssueDevice(ctx, f.actorA, KindPWA)
	mustOK(t, err, "issue device")
	id, secret, err := devicekey.Parse(issued.APIKey)
	mustOK(t, err, "parse issued key")
	if id != issued.ID {
		t.Fatalf("key id %d != device id %d", id, issued.ID)
	}

	var hash []byte
	mustOK(t, f.db.QueryRow(`SELECT api_key_hash FROM devices WHERE id = $1`, issued.ID).Scan(&hash), "read hash")
	if len(hash) != 32 || !bytes.Equal(hash, devicekey.Hash(storeSalt, secret)) {
		t.Fatalf("stored hash = %x, want HMAC of the secret", hash)
	}
	var n int
	mustOK(t, f.db.QueryRow(`SELECT count(*) FROM devices WHERE position(convert_to($1,'UTF8') in api_key_hash) > 0 OR api_key_hash = convert_to($1,'UTF8')`, issued.APIKey).Scan(&n), "scan for key")
	if n != 0 {
		t.Fatal("plaintext key found in devices")
	}
	if _, err := f.svc.IssueDevice(ctx, f.actorA, "broker"); !errors.Is(err, ErrValidation) {
		t.Fatalf("bad kind = %v, want ErrValidation", err)
	}
}

func TestDeviceRotateReplacesHashAndInvalidatesOldKey(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	issued, err := f.svc.IssueDevice(ctx, f.actorA, KindTracker)
	mustOK(t, err, "issue")
	var before []byte
	mustOK(t, f.db.QueryRow(`SELECT api_key_hash FROM devices WHERE id = $1`, issued.ID).Scan(&before), "read")

	rotated, err := f.svc.RotateDevice(ctx, f.actorA, issued.ID)
	mustOK(t, err, "rotate")
	var after []byte
	mustOK(t, f.db.QueryRow(`SELECT api_key_hash FROM devices WHERE id = $1`, issued.ID).Scan(&after), "read")
	if bytes.Equal(before, after) || len(after) != 32 {
		t.Fatal("rotate did not replace the 32-byte hash")
	}
	if _, err := AuthenticateDeviceKey(ctx, f.db, storeSalt, issued.APIKey); !errors.Is(err, ErrDeviceKeyInvalid) {
		t.Fatalf("old key after rotate = %v, want ErrDeviceKeyInvalid", err)
	}
	if _, err := AuthenticateDeviceKey(ctx, f.db, storeSalt, rotated.APIKey); err != nil {
		t.Fatalf("new key after rotate: %v", err)
	}
	if _, err := f.svc.RotateDevice(ctx, f.actorB, issued.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rotate across fleets = %v, want ErrNotFound", err)
	}
}

func TestDeviceRevokeKeepsFirstTimestamp(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	issued, err := f.svc.IssueDevice(ctx, f.actorA, KindPWA)
	mustOK(t, err, "issue")

	first, err := f.svc.RevokeDevice(ctx, f.actorA, issued.ID)
	mustOK(t, err, "first revoke")
	if first.RevokedAt == nil {
		t.Fatal("revoked_at not set")
	}
	f.tick()
	second, err := f.svc.RevokeDevice(ctx, f.actorA, issued.ID)
	mustOK(t, err, "second revoke")
	if second.RevokedAt == nil || !second.RevokedAt.Equal(*first.RevokedAt) {
		t.Fatalf("second revoke changed timestamp: %v vs %v", second.RevokedAt, first.RevokedAt)
	}
	if _, err := f.svc.RotateDevice(ctx, f.actorA, issued.ID); !errors.Is(err, ErrDeviceRevoked) {
		t.Fatalf("rotate revoked device = %v, want ErrDeviceRevoked", err)
	}
}

func TestCheckDeviceActive(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	issued, err := f.svc.IssueDevice(ctx, f.actorA, KindPWA)
	mustOK(t, err, "issue")
	mustOK(t, CheckDeviceActive(ctx, f.db, issued.ID), "active device")
	if err := CheckDeviceActive(ctx, f.db, 999999); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("unknown device = %v, want ErrDeviceNotFound", err)
	}
	_, err = f.svc.RevokeDevice(ctx, f.actorA, issued.ID)
	mustOK(t, err, "revoke")
	if err := CheckDeviceActive(ctx, f.db, issued.ID); !errors.Is(err, ErrDeviceRevoked) {
		t.Fatalf("revoked device = %v, want ErrDeviceRevoked", err)
	}
}

func TestAuthenticateDeviceKey(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	issued, err := f.svc.IssueDevice(ctx, f.actorA, KindPWA)
	mustOK(t, err, "issue")

	d, err := AuthenticateDeviceKey(ctx, f.db, storeSalt, issued.APIKey)
	mustOK(t, err, "valid key")
	if d.ID != issued.ID || d.FleetID != f.fleetA {
		t.Fatalf("authenticated device = %+v", d)
	}
	if _, err := AuthenticateDeviceKey(ctx, f.db, storeSalt, "fo_dev_1_short"); !errors.Is(err, ErrDeviceKeyMalformed) {
		t.Fatalf("malformed key = %v, want ErrDeviceKeyMalformed", err)
	}
	wrong := devicekey.Format(issued.ID, bytes.Repeat([]byte{7}, 32))
	if _, err := AuthenticateDeviceKey(ctx, f.db, storeSalt, wrong); !errors.Is(err, ErrDeviceKeyInvalid) {
		t.Fatalf("wrong secret = %v, want ErrDeviceKeyInvalid", err)
	}
	unknown := devicekey.Format(987654, bytes.Repeat([]byte{7}, 32))
	if _, err := AuthenticateDeviceKey(ctx, f.db, storeSalt, unknown); !errors.Is(err, ErrDeviceKeyInvalid) {
		t.Fatalf("unknown id = %v, want ErrDeviceKeyInvalid", err)
	}
	_, err = f.svc.RevokeDevice(ctx, f.actorA, issued.ID)
	mustOK(t, err, "revoke")
	if _, err := AuthenticateDeviceKey(ctx, f.db, storeSalt, issued.APIKey); !errors.Is(err, ErrDeviceRevoked) {
		t.Fatalf("revoked device = %v, want ErrDeviceRevoked", err)
	}
}
