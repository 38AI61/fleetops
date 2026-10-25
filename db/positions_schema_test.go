package db_test

import (
	"database/sql"
	"testing"

	"fleetops/internal/testinfra"
)

// TestPositionsSchemaIsPartitionedParent checks the GF-68 and GF-69 shape on a
// migrated PostGIS database: partitioned parent, primary key, no DEFAULT
// partition, the vehicle FK, and the vehicles_latest columns and indexes.
func TestPositionsSchemaIsPartitionedParent(t *testing.T) {
	conn := testinfra.NewMigratedDB(t)

	var strategy string
	if err := conn.QueryRow(`
		SELECT p.partstrat::text FROM pg_partitioned_table p
		JOIN pg_class c ON c.oid = p.partrelid WHERE c.relname = 'positions'`).Scan(&strategy); err != nil {
		t.Fatalf("positions is not a partitioned table: %v", err)
	}
	if strategy != "r" {
		t.Errorf("positions partstrat = %q, want \"r\" (RANGE)", strategy)
	}

	assertPrimaryKey(t, conn, "positions", []string{"server_ts", "msg_id"})

	var defaults int
	if err := conn.QueryRow(`
		SELECT count(*) FROM pg_class c
		JOIN pg_inherits i ON i.inhrelid = c.oid
		JOIN pg_class parent ON parent.oid = i.inhparent
		WHERE parent.relname = 'positions'
		  AND pg_get_expr(c.relpartbound, c.oid) = 'DEFAULT'`).Scan(&defaults); err != nil {
		t.Fatalf("default partition query: %v", err)
	}
	if defaults != 0 {
		t.Errorf("positions has %d DEFAULT partition(s), want 0", defaults)
	}

	var fkTarget sql.NullString
	if err := conn.QueryRow(`
		SELECT confrelid::regclass::text FROM pg_constraint
		WHERE conrelid = 'positions'::regclass AND contype = 'f'
		  AND conkey = ARRAY[(SELECT attnum FROM pg_attribute WHERE attrelid = 'positions'::regclass AND attname = 'vehicle_id')]`).Scan(&fkTarget); err != nil {
		t.Fatalf("vehicle_id foreign key: %v", err)
	}
	if fkTarget.String != "vehicles" {
		t.Errorf("positions.vehicle_id references %q, want vehicles", fkTarget.String)
	}

	for _, col := range []string{"vehicle_id", "fleet_id", "geom", "speed_kmh", "heading_deg", "device_ts", "server_ts"} {
		assertColumn(t, conn, "vehicles_latest", col)
	}
	for _, idx := range []string{"vehicles_latest_geom_gix", "vehicles_latest_fleet_idx"} {
		var ok bool
		if err := conn.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE tablename = 'vehicles_latest' AND indexname = $1)`, idx).Scan(&ok); err != nil {
			t.Fatalf("index %s: %v", idx, err)
		}
		if !ok {
			t.Errorf("index %s missing on vehicles_latest", idx)
		}
	}
}

func assertPrimaryKey(t *testing.T, conn *sql.DB, table string, want []string) {
	t.Helper()
	rows, err := conn.Query(`
		SELECT a.attname FROM pg_index i
		JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
		WHERE i.indrelid = $1::regclass AND i.indisprimary
		ORDER BY k.ord`, table)
	if err != nil {
		t.Fatalf("primary key of %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, name)
	}
	if len(got) != len(want) {
		t.Fatalf("primary key of %s = %v, want %v", table, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("primary key of %s = %v, want %v", table, got, want)
		}
	}
}

func assertColumn(t *testing.T, conn *sql.DB, table, column string) {
	t.Helper()
	var ok bool
	if err := conn.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2)`, table, column).Scan(&ok); err != nil {
		t.Fatalf("column %s.%s: %v", table, column, err)
	}
	if !ok {
		t.Errorf("column %s.%s missing", table, column)
	}
}
