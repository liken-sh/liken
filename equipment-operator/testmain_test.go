package main

import (
	"os"
	"testing"
)

// TestMain sets the package variables of the tests before any test
// starts a goroutine that reads them.
//
// Most tests call t.Parallel, and they read package variables. So a
// test that writes one, such as a stub of discover or a shorter
// duration, must not call t.Parallel, or the write is a data race.
//
// A real device search would use the network of whoever runs the
// tests, so discover fails the run unless a test stubs it. See
// nodiscovery_test.go.
func TestMain(m *testing.M) {
	discover = unstubbedDiscover
	os.Exit(m.Run())
}
