package testinfra

import (
	"testing"
	"time"
)

func TestEnsurePositionsPartitionsIdempotent(t *testing.T) {
	conn := NewMigratedDB(t)
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	to := time.Date(2027, 1, 3, 0, 0, 0, 0, time.UTC)

	EnsurePositionsPartitions(t, conn, from, to)
	EnsurePositionsPartitions(t, conn, from, to)

	want := map[string][2]string{
		"positions_2026_11": {"2026-11-01 00:00:00+00", "2026-12-01 00:00:00+00"},
		"positions_2026_12": {"2026-12-01 00:00:00+00", "2027-01-01 00:00:00+00"},
		"positions_2027_01": {"2027-01-01 00:00:00+00", "2027-02-01 00:00:00+00"},
	}
	for name, bounds := range want {
		var got string
		if err := conn.QueryRow(`SELECT pg_get_expr(c.relpartbound, c.oid) FROM pg_class c WHERE c.relname = $1`, name).Scan(&got); err != nil {
			t.Fatalf("partition %s missing: %v", name, err)
		}
		wantExpr := "FOR VALUES FROM ('" + bounds[0] + "') TO ('" + bounds[1] + "')"
		if got != wantExpr {
			t.Errorf("%s bound = %q, want %q", name, got, wantExpr)
		}
	}

	var children int
	if err := conn.QueryRow(`SELECT count(*) FROM pg_inherits i JOIN pg_class p ON p.oid = i.inhparent WHERE p.relname = 'positions'`).Scan(&children); err != nil {
		t.Fatalf("pg_inherits: %v", err)
	}
	if children != 3 {
		t.Fatalf("positions children = %d, want 3", children)
	}
}
