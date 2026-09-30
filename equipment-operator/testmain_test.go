package main

import (
	"os"
	"testing"
)

// TestMain sets the package-wide test values before any test starts a
// goroutine that reads them.
//
// Most tests call t.Parallel. Go runs those tests at the same time,
// after the tests that do not call it have finished. So a test that
// writes a package variable, such as a stub of discover or a duration
// that a vivid test shortens, must not call t.Parallel. The parallel
// tests read those variables, and the write would be a data race that
// -race reports only when the timing lines up.
//
// A real search for devices would reach the network of whoever runs the
// tests, so the search fails the run unless a test stubs it; see
// nodiscovery_test.go.
func TestMain(m *testing.M) {
	discover = unstubbedDiscover
	os.Exit(m.Run())
}
