package main

// A real search for devices sends SSDP and mDNS queries onto the local
// network of whoever runs the tests, and every UPnP renderer there
// answers. So no test runs one: a test that starts the loop turns
// discovery off, or stubs the search when discovery is what it tests,
// and the search every other test would run fails the run.

import (
	"context"
	"time"

	"github.com/liken-sh/equipment-operator/wiim"
)

// unstubbedDiscover stands in for wiim.Discover in a test that did not
// stub the search. It panics, because discovery runs on its own
// goroutine and holds no *testing.T to fail. Run the package with -v
// to see which tests were running; several parallel tests can run at
// the same time.
func unstubbedDiscover(context.Context, time.Duration) []wiim.Device {
	panic("a test ran a search for devices on the local network; turn discovery off with networkDiscoveryOff, or stub discover")
}
