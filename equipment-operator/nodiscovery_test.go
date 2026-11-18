package main

// A real device search sends SSDP and mDNS queries to the local network
// of whoever runs the tests, so no test runs one. TestMain sets discover
// to unstubbedDiscover, and a test that starts the discovery loop must
// turn discovery off or stub discover.

import (
	"context"
	"time"

	"github.com/liken-sh/liken/equipment-operator/wiim"
)

// unstubbedDiscover panics because the discovery loop runs on its own
// goroutine and has no *testing.T to fail. The panic cannot name the
// test that started the loop, so run the package with -v to find it.
func unstubbedDiscover(context.Context, time.Duration) []wiim.Device {
	panic("a test searched the local network for devices; turn discovery off with networkDiscoveryOff, or stub discover")
}
