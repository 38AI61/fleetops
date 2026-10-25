package presence

import (
	"os"
	"testing"

	"fleetops/internal/testinfra"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testinfra.TerminateRedis()
	os.Exit(code)
}
