package alerts

import (
	"testing"

	"fleetops/internal/testinfra"
)

// checkViolation is SQLSTATE 23514, which the alerts guard and the CHECK raise.
const checkViolation = "23514"

// TestAlertsAckTrigger covers GF-295 and D-09 on raw SQL: the first ack passes,
// the second is rejected, and every other UPDATE, DELETE and TRUNCATE is rejected.
func TestAlertsAckTrigger(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	fleet := newFleet(t, db, "ack")
	admin := newUser(t, db, fleet, "admin")
	other := newUser(t, db, fleet, "manager")

	t.Run("first ack passes", func(t *testing.T) {
		id := newAlert(t, db, fleet, KindDeviceSilent, SeverityWarning)
		if _, err := db.Exec(`UPDATE alerts SET acked_by = $2, acked_at = now() WHERE id = $1`, id, admin); err != nil {
			t.Fatalf("first ack: %v", err)
		}
	})

	t.Run("second ack is rejected", func(t *testing.T) {
		id := newAlert(t, db, fleet, KindDeviceSilent, SeverityWarning)
		if _, err := db.Exec(`UPDATE alerts SET acked_by = $2, acked_at = now() WHERE id = $1`, id, admin); err != nil {
			t.Fatalf("first ack: %v", err)
		}
		_, err := db.Exec(`UPDATE alerts SET acked_by = $2, acked_at = now() WHERE id = $1`, id, other)
		if got := sqlState(err); got != checkViolation {
			t.Fatalf("second ack state = %q (%v), want %s", got, err, checkViolation)
		}
	})

	t.Run("severity and payload are frozen", func(t *testing.T) {
		id := newAlert(t, db, fleet, KindGeofenceExit, SeverityCritical)
		_, err := db.Exec(`UPDATE alerts SET severity = 'warning' WHERE id = $1`, id)
		if got := sqlState(err); got != checkViolation {
			t.Fatalf("severity update state = %q (%v), want %s", got, err, checkViolation)
		}
		_, err = db.Exec(`UPDATE alerts SET payload = '{"x":1}'::jsonb, acked_by = $2, acked_at = now() WHERE id = $1`, id, admin)
		if got := sqlState(err); got != checkViolation {
			t.Fatalf("payload with ack state = %q (%v), want %s", got, err, checkViolation)
		}
	})

	t.Run("delete is rejected", func(t *testing.T) {
		id := newAlert(t, db, fleet, KindDeviceSilent, SeverityWarning)
		_, err := db.Exec(`DELETE FROM alerts WHERE id = $1`, id)
		if got := sqlState(err); got != checkViolation {
			t.Fatalf("delete state = %q (%v), want %s", got, err, checkViolation)
		}
	})

	t.Run("truncate is rejected", func(t *testing.T) {
		newAlert(t, db, fleet, KindDeviceSilent, SeverityWarning)
		_, err := db.Exec(`TRUNCATE alerts`)
		if got := sqlState(err); got != checkViolation {
			t.Fatalf("truncate state = %q (%v), want %s", got, err, checkViolation)
		}
	})

	t.Run("acked_by without acked_at is rejected by CHECK", func(t *testing.T) {
		_, err := db.Exec(`
			INSERT INTO alerts (fleet_id, kind, severity, payload, acked_by, acked_at)
			VALUES ($1, $2, $3, '{}'::jsonb, $4, NULL)`,
			fleet, KindDeviceSilent, SeverityWarning, admin)
		if got := sqlState(err); got != checkViolation {
			t.Fatalf("half ack insert state = %q (%v), want %s", got, err, checkViolation)
		}
	})
}
