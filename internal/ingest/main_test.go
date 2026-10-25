package ingest

import (
	"os"
	"testing"

	"fleetops/internal/testinfra"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testinfra.TerminateNATS()
	os.Exit(code)
}
