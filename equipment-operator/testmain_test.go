package main

import (
	"os"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/denon"
)

// TestMain sets the package-wide test values before any test starts a
// goroutine that reads them.
//
// Most tests call t.Parallel. Go runs those tests at the same time,
// after the tests that do not call it have finished. So a test that
// writes a package variable, directly or through a helper such as
// shorten, fastWake, fastPower, fastLift, or noDiscovery, must not call
// t.Parallel. The parallel tests read those variables, and the write
// would be a data race that -race reports only when the timing lines
// up.
//
// A real search for devices would reach the network of whoever runs the
// tests, so the search fails the run unless a test stubs it; see
// nodiscovery_test.go.
//
// Almost every test against the fake Denon waits for the survey, and
// the fake answers the connect queries in one burst on loopback. The
// quiet stretch that ends the survey is a second on a real receiver,
// so without the shorter value each such test waits a second for
// nothing.
func TestMain(m *testing.M) {
	discover = unstubbedDiscover
	denon.SurveyQuiet = 100 * time.Millisecond
	os.Exit(m.Run())
}
