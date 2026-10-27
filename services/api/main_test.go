package main

import (
	"os"
	"testing"

	"fleetops/internal/proofs/prooftest"
	"fleetops/internal/testinfra"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testinfra.TerminateNATS()
	prooftest.TerminateMinIO()
	os.Exit(code)
}
