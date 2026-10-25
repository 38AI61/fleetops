package testinfra

import (
	"database/sql"
	"testing"

	_ "github.com/lib/pq"
)

func TestStartPostgisHasPostGIS(t *testing.T) {
	dsn := StartPostgis(t)

	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer conn.Close()

	var version string
	if err := conn.QueryRow("SELECT postgis_lib_version()").Scan(&version); err != nil {
		t.Fatalf("postgis_lib_version: %v", err)
	}
	if version == "" {
		t.Fatal("postgis_lib_version returned empty string")
	}
}
