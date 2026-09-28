package main

// The EQUIPMENT_NETWORK_DISCOVERY setting, run through serve the way
// the Deployment runs it. A search here is a stub that counts its calls
// and finds one amp, so a test that turns discovery off proves the
// operator never searched, and a test that leaves it on proves the
// operator searched and created the amp's Receiver.

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/wiim"
)

// countedDiscovery replaces the search with one that finds one amp
// until the test ends, and answers the number of searches that ran.
func countedDiscovery(t *testing.T, found wiim.Device) *atomic.Int64 {
	t.Helper()
	searches := &atomic.Int64{}
	restore := discover
	discover = func(context.Context, time.Duration) []wiim.Device {
		searches.Add(1)
		return []wiim.Device{found}
	}
	t.Cleanup(func() { discover = restore })
	return searches
}

// runServeUntil runs serve with one configuration until ready answers
// true, and then stops it and waits for it to return. serve returns
// only after every goroutine it started has stopped, discovery
// included, so what the test reads after this is final.
func runServeUntil(t *testing.T, api *fakeAPI, config settings, ready func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		mustSucceed(t, serve(ctx, api.client, config, testMetrics(t)))
	}()
	ready()
	cancel()
	select {
	case <-stopped:
	case <-time.After(testTimeout):
		t.Fatal("serve did not stop")
	}
}

// With discovery off, the operator runs no search and creates no
// Receiver, and a Receiver a person declared still reaches its
// receiver. With discovery on, the same operator searches and creates
// a Receiver for the amp no Receiver names.
func TestNetworkDiscoveryOffSearchesForNothing(t *testing.T) {
	amp := wiim.Device{UUID: firstUUID, Address: "192.0.2.1"}
	created := discoveredName(amp.UUID)
	cases := []struct {
		name     string
		off      bool
		searched bool
		created  bool
		// settled waits until the operator has done what the case
		// expects of it: reached the declared receiver, and with
		// discovery on, created the amp's Receiver as well.
		settled func(*testing.T, *fakeAPI)
	}{
		{name: "on", off: false, searched: true, created: true, settled: func(t *testing.T, api *fakeAPI) {
			api.waitForStatus(t, connected)
			waitForApply(t, api, created)
		}},
		{name: "off", off: true, searched: false, created: false, settled: func(t *testing.T, api *fakeAPI) {
			api.waitForStatus(t, connected)
		}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			searches := countedDiscovery(t, amp)
			api := startFakeAPI(t)
			equipment := startFakeDenon(t)
			api.setReceivers(testReceiver("theater", equipment.address()))
			config := settings{busAddress: "127.0.0.1:1", networkDiscoveryOff: one.off}

			runServeUntil(t, api, config, func() { one.settled(t, api) })

			mustMatch(t, searches.Load() > 0, one.searched)
			mustMatch(t, slices.Contains(api.appliedReceivers(), created), one.created)
		})
	}
}

// waitForApply waits until the operator has applied the named Receiver.
func waitForApply(t *testing.T, api *fakeAPI, name string) {
	t.Helper()
	deadline := time.After(testTimeout)
	for !slices.Contains(api.appliedReceivers(), name) {
		select {
		case <-deadline:
			t.Fatalf("the operator never applied Receiver %s; it applied %v", name, api.appliedReceivers())
		case <-time.After(time.Millisecond):
		}
	}
}

// The operator states at the start of its loop that discovery is off,
// and names the setting, so the reason no Receiver appears is in the
// log.
func TestNetworkDiscoveryOffWritesOneLine(t *testing.T) {
	searches := countedDiscovery(t, wiim.Device{UUID: firstUUID, Address: "192.0.2.1"})
	api := startFakeAPI(t)
	api.setReceivers()
	operator := startController(t, api)
	operator.networkDiscoveryOff = true
	log := &logBuffer{}
	operator.log = log
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		operator.run(ctx)
	}()
	deadline := time.After(testTimeout)
	for log.lines()[0] == "" {
		select {
		case <-deadline:
			t.Fatal("the loop wrote no line")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(testTimeout):
		t.Fatal("the loop did not stop")
	}

	lines := log.lines()
	mustMatch(t, len(lines), 1)
	mustMatch(t, strings.HasPrefix(lines[0], networkDiscoveryVariable+" is off"), true)
	mustMatch(t, searches.Load(), int64(0))
}
