package ingest

import (
	"errors"
	"testing"

	"github.com/lib/pq"
)

// TestMissingPartitionSQLSTATE pins GF-90: an insert whose server_ts has no
// child partition fails with 23514 from the positions insert itself. The
// detection relies on positions having no CHECK constraint, asserted below.
func TestMissingPartitionSQLSTATE(t *testing.T) {
	e := newFlushEnv(t)
	vehicleID, deviceID := e.pairedVehicle(0)

	var checks int
	if err := e.conn.QueryRow(`SELECT count(*) FROM pg_constraint WHERE conrelid = 'positions'::regclass AND contype = 'c'`).Scan(&checks); err != nil {
		t.Fatalf("count positions checks: %v", err)
	}
	if checks != 0 {
		t.Fatalf("positions has %d CHECK constraints; GF-90 requires none", checks)
	}

	// flushEnv creates the flushBase month only; this instant has no child.
	missing := flushBase.AddDate(0, 3, 0)
	m := e.msg(deviceID, missing, 41.0, 29.0, 10)
	_, err := e.conn.Exec(`
INSERT INTO positions (msg_id, fleet_id, vehicle_id, geom, speed_kmh, heading_deg, accuracy_m, device_ts, server_ts)
VALUES ($1, $2, $3, ST_SetSRID(ST_MakePoint(29, 41), 4326)::geography, 0, 0, 5, $4, $4)`,
		m.MsgID.String(), e.fleet, vehicleID, missing)
	if err == nil {
		t.Fatal("insert into missing partition succeeded")
	}
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		t.Fatalf("error is %T, want *pq.Error: %v", err, err)
	}
	if pqErr.Code != "23514" {
		t.Fatalf("SQLSTATE = %s, want 23514", pqErr.Code)
	}
}

// TestNoDefaultPartition pins D-14: no child of positions is a DEFAULT partition.
func TestNoDefaultPartition(t *testing.T) {
	conn := newFlushEnv(t).conn

	var defaults int
	err := conn.QueryRow(`
SELECT count(*) FROM pg_inherits i
JOIN pg_class c ON c.oid = i.inhrelid
WHERE i.inhparent = 'positions'::regclass
  AND c.relispartition
  AND pg_get_expr(c.relpartbound, c.oid) = 'DEFAULT'`).Scan(&defaults)
	if err != nil {
		t.Fatalf("query default partitions: %v", err)
	}
	if defaults != 0 {
		t.Fatalf("%d DEFAULT partitions on positions; D-14 forbids them", defaults)
	}
}
