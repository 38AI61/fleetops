package db

import (
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var migrationName = regexp.MustCompile(`^(\d{3})_[a-z0-9_]+\.(up|down)\.sql$`)

func TestMigrationFilesArePaired(t *testing.T) {
	entries, err := fs.ReadDir(Migrations, "migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}

	ups := map[int]string{}
	downs := map[int]string{}
	for _, e := range entries {
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			t.Errorf("migration %q does not match NNN_snake_name.(up|down).sql", e.Name())
			continue
		}
		version, _ := strconv.Atoi(m[1])
		switch m[2] {
		case "up":
			if prev, dup := ups[version]; dup {
				t.Errorf("duplicate up migration for version %03d: %q and %q", version, prev, e.Name())
			}
			ups[version] = e.Name()
		case "down":
			if prev, dup := downs[version]; dup {
				t.Errorf("duplicate down migration for version %03d: %q and %q", version, prev, e.Name())
			}
			downs[version] = e.Name()
		}
	}

	if len(ups) == 0 {
		t.Fatal("no migrations found")
	}
	for v, name := range ups {
		if _, ok := downs[v]; !ok {
			t.Errorf("%q has no matching .down.sql", name)
		}
	}
	for v, name := range downs {
		if _, ok := ups[v]; !ok {
			t.Errorf("%q has no matching .up.sql", name)
		}
	}
	for v := 1; v <= len(ups); v++ {
		if _, ok := ups[v]; !ok {
			t.Errorf("versions not contiguous from 001: missing %03d", v)
		}
	}
}

func TestMigrationFilesAreTransactionSafe(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)\b(BEGIN|COMMIT|CONCURRENTLY)\b`)

	err := fs.WalkDir(Migrations, "migrations", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(Migrations, p)
		if err != nil {
			return err
		}
		if m := forbidden.Find(body); m != nil {
			t.Errorf("%s contains forbidden keyword %q (GF-5)", path.Base(p), strings.ToUpper(string(m)))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk migrations: %v", err)
	}
}
