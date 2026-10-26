package hub

import (
	"os"
	"testing"

	"fleetops/internal/testinfra"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testinfra.TerminateNATS()
	testinfra.TerminateRedis()
	os.Exit(code)
}
