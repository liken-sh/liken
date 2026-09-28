package main

// A real search for devices sends SSDP and mDNS queries onto the local
// network of whoever runs the tests, and every UPnP renderer there
// answers. So no test runs one: a test that starts discovery stubs the
// search with noDiscovery or its own stub, and the search every other
// test would run fails the run.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/wiim"
)

func TestMain(m *testing.M) {
	discover = unstubbedDiscover
	os.Exit(m.Run())
}

// unstubbedDiscover stands in for wiim.Discover in a test that did not
// stub the search. It panics, because discovery runs on its own
// goroutine and holds no *testing.T to fail. Run the package with -v
// to see which test was running.
func unstubbedDiscover(context.Context, time.Duration) []wiim.Device {
	panic("a test ran a search for devices on the local network; stub it with noDiscovery(t)")
}

// noDiscovery makes every search find nothing until the test ends.
func noDiscovery(t *testing.T) {
	t.Helper()
	restore := discover
	discover = func(context.Context, time.Duration) []wiim.Device { return nil }
	t.Cleanup(func() { discover = restore })
}
