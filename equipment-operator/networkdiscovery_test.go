package main

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/wiim"
)

// countedDiscovery replaces the search with a stub that returns found
// until the test ends. It returns the number of searches that ran.
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

// runServeUntil runs serve with config until ready returns, then stops
// serve and waits for it to return. serve returns only after every
// goroutine it started has stopped, discovery included, so what the
// test reads afterward is final.
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
		t.Fatal("serve was still running after its context was canceled")
	}
}

// The test runs serve the way the Deployment runs it. With discovery
// off, the operator runs no search and creates no Receiver, and a
// Receiver that a person declared still connects to its receiver. With
// discovery on, the operator searches and creates a Receiver for the
// amp that no Receiver declares.
func TestNetworkDiscoveryOffSearchesForNothing(t *testing.T) {
	amp := wiim.Device{UUID: firstUUID, Address: "192.0.2.1"}
	created := discoveredName(amp.UUID)
	cases := []struct {
		name     string
		off      bool
		searched bool
		created  bool
		// settled waits until the operator has connected to the declared
		// receiver and, with discovery on, has also created the amp's
		// Receiver.
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
			synctest.Test(t, func(t *testing.T) {
				searches := countedDiscovery(t, amp)
				api := startFakeAPI(t)
				equipment := startFakeDenon(t)
				api.setReceivers(testReceiver("theater", equipment.address()))
				config := settings{namespace: testNamespace, networkDiscoveryOff: one.off, dial: testNetwork.dial, listen: testNetwork.bind}

				runServeUntil(t, api, config, func() { one.settled(t, api) })

				mustMatch(t, searches.Load() > 0, one.searched)
				mustMatch(t, slices.Contains(api.appliedReceivers(), created), one.created)
			})
		})
	}
}

// waitForApply waits until the operator has applied the Receiver
// called name.
func waitForApply(t *testing.T, api *fakeAPI, name string) {
	t.Helper()
	deadline := time.After(testTimeout)
	for !slices.Contains(api.appliedReceivers(), name) {
		select {
		case <-deadline:
			t.Fatalf("the applied Receivers are %v, want Receiver %s among them", api.appliedReceivers(), name)
		case <-time.After(time.Millisecond):
		}
	}
}

// At the start of its loop, the operator logs one line that says
// discovery is off and names the setting, so the log shows why no
// Receiver appears.
func TestNetworkDiscoveryOffWritesOneLine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
			t.Fatal("the loop was still running after its context was canceled")
		}

		lines := log.lines()
		mustMatch(t, len(lines), 1)
		mustMatch(t, strings.HasPrefix(lines[0], networkDiscoveryVariable+" is off"), true)
		mustMatch(t, searches.Load(), int64(0))
	})
}
