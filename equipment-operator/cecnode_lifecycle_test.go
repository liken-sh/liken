package main

// Tests of the node workload's adapter lifecycle. cecnode_test.go has
// the fixtures.

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
)

// joinedNode starts a node workload in Control mode and waits until it
// has finished a scan.
func joinedNode(t *testing.T, api *cecAPI, wire *cectest.Bus) (*cectest.Adapter, context.CancelFunc, <-chan error) {
	t.Helper()
	adapter, device := usbAdapter(wire)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	node, err := newCECNode(api.client, testNamespace, "node-1", device)
	mustSucceed(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		done <- node.run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(cecStopWrite + testTimeout):
			t.Error("the node workload did not stop")
		}
	})
	api.scanned(t, "node-1")
	return adapter, cancel, done
}

// A stop cancels entry writes that wait to retry after a 429, so the
// workload exits within its grace period. The write of the Stopped
// entry waits at most cecStopWrite.
func TestAStopEndsAWriteThatWaitsOutA429(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := fakeCECBus()
		_, cancel, done := joinedNode(t, api, wire)
		api.mutex.Lock()
		api.throttlingEntries = true
		api.mutex.Unlock()

		wire.Send(cec.ActiveSource(8, 0x1500))

		api.waitUntil(t, "an entry write to meet the 429", func() bool {
			api.mutex.Lock()
			defer api.mutex.Unlock()
			return api.entryThrottles > 0
		})
		began := time.Now()

		cancel()

		select {
		case <-done:
		case <-time.After(2 * cecStopWrite):
			t.Fatal("the node workload did not stop")
		}
		// cec.Read notices the cancel within its 250 ms wait, and then
		// the Stopped write waits at most cecStopWrite.
		if took := time.Since(began); took > cecStopWrite+time.Second/4 {
			t.Errorf("the node workload stopped after %s, want at most %s", took, cecStopWrite+time.Second/4)
		}
	})
}

// After a stop, a write that is already in flight completes before the
// Stopped entry is written, so no earlier entry can overwrite it.
func TestAStopLetsAWriteInFlightLandBeforeTheStoppedEntry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := fakeCECBus()
		_, cancel, done := joinedNode(t, api, wire)
		release := make(chan struct{})
		arrived := make(chan struct{}, 64)
		stoppedEntry := make(chan struct{}, 1)
		api.mutex.Lock()
		api.holdingEntries, api.heldEntry, api.stoppedEntry = release, arrived, stoppedEntry
		api.mutex.Unlock()
		wire.Send(cec.ActiveSource(8, 0x1500))
		select {
		case <-arrived:
		case <-time.After(testTimeout):
			t.Fatal("no entry write reached the API server")
		}

		cancel()
		// A workload that does not wait for the earlier write sends its
		// Stopped entry now, and the test then releases the earlier write
		// to overwrite it. A workload that waits gets the release after
		// 300 ms.
		select {
		case <-stoppedEntry:
		case <-time.After(300 * time.Millisecond):
		}
		close(release)

		select {
		case <-done:
		case <-time.After(testTimeout):
			t.Fatal("the node workload did not stop")
		}
		api.holds.Wait()
		entry, held := api.entry("den", "node-1")
		mustMatch(t, held, true)
		mustMatch(t, entry.State, AdapterStopped)
	})
}

// An unchanged entry is never written again. The Deployment judges
// whether an entry is current from the node workload's pod, not from
// the entry's age.
func TestAnIdleNodeWritesNoEntry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		joinedNode(t, api, fakeCECBus())
		before := api.writesOf("node-1")

		time.Sleep(10 * time.Minute)

		mustMatch(t, api.writesOf("node-1"), before)
		entry, _ := api.entry("den", "node-1")
		if _, err := time.Parse(time.RFC3339, entry.ReportedAt); err != nil {
			t.Errorf("parsing reportedAt %q: %v", entry.ReportedAt, err)
		}
	})
}

// A stopping pod releases the adapter's logical address and stops
// following the bus. Otherwise the kernel keeps the claim, and the
// adapter keeps answering the TV for a machine that no pod serves.
func TestAStoppedNodeReleasesTheAdapter(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		adapter, cancel, done := joinedNode(t, api, fakeCECBus())

		cancel()

		mustSucceed(t, waitForExit(t, done))
		if held := adapter.Logical(); len(held) != 0 || adapter.Follows() {
			t.Errorf("a stopped node leaves the adapter holding %v, following %v", held, adapter.Follows())
		}
		entry, _ := api.entry("den", "node-1")
		mustMatch(t, entry.State, AdapterStopped)
		if entry.LogicalAddress != nil || !strings.HasPrefix(entry.Message, "the node workload stopped: context canceled") {
			t.Errorf("the entry is %+v, want no logical address and a message that starts with the node workload stopped", entry)
		}
	})
}

func TestAnUnpluggedAdapterLeavesAStoppedEntry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		adapter, _, done := joinedNode(t, api, fakeCECBus())

		adapter.Unplug()

		if err := waitForExit(t, done); !cec.IsGone(err) {
			t.Errorf("ended with %v, want ENODEV", err)
		}
		entry, _ := api.entry("den", "node-1")
		mustMatch(t, entry.State, AdapterStopped)
		if !strings.HasPrefix(entry.Message, "the adapter failed: ") || !strings.Contains(entry.Message, "no such device") {
			t.Errorf("the message is %q, want one that starts with the adapter failed and contains no such device", entry.Message)
		}
	})
}

func waitForExit(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(testTimeout):
		t.Fatal("the node workload did not stop")
		return nil
	}
}

// When no CECBus lists the machine, and the workload cannot create one
// because a CECBus with the machine's name exists, the adapter leaves
// its bus.
func TestAnAdapterLeavesTheBusWhenNoBusNamesItsMachine(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		adapter, _, _ := joinedNode(t, api, fakeCECBus())

		api.putBus(CECBus{Metadata: ObjectMeta{Name: "node-1"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-2"}}}})
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-2", Display: "acm-0001-receiver"}))

		api.waitUntil(t, "the adapter to leave the bus", func() bool {
			_, held := api.entry("den", "node-1")
			return !held && len(adapter.Logical()) == 0 && !adapter.Follows()
		})
	})
}

// When the Display cannot be read, the adapter keeps its physical
// address and stays joined.
func TestAFailedDisplayReadKeepsTheAdapterJoined(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := fakeCECBus()
		adapter, _, _ := joinedNode(t, api, wire)
		claimsBefore := adapter.Claims()

		api.removeDisplay("acm-0001-receiver")
		api.nudge()

		entry := api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.Message != "" })
		mustMatch(t, entry.Message, "reading Display acm-0001-receiver: not found")
		mustMatch(t, entry.State, AdapterScanned)
		mustMatch(t, entry.PhysicalAddress, "1.3.0.0")
		if held := adapter.Logical(); len(held) != 1 || held[0] != 4 || adapter.Claims() != claimsBefore {
			t.Errorf("the adapter joined again: holds %v", held)
		}
	})
}

func TestAFailedJoinIsTriedAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := fakeCECBus()
		for _, address := range []cec.LogicalAddress{4, 8, 11} {
			wire.Add(cectest.Peer{Logical: address, Physical: 0x2000, PrimaryType: 4})
		}
		_, device := usbAdapter(wire)
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		startNode(t, api, "node-1", device)
		api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterJoining })

		wire.Remove(8)
		time.Sleep(cecRetryFirst)

		entry := api.scanned(t, "node-1")
		mustMatch(t, *entry.LogicalAddress, 8)
	})
}

func TestTheRetryWaitGrowsToItsBound(t *testing.T) {
	cases := []struct {
		last time.Duration
		next time.Duration
	}{
		{0, cecRetryFirst},
		{cecRetryFirst, 2 * cecRetryFirst},
		{cecRetryMax, cecRetryMax},
		{cecRetryMax * 3 / 4, cecRetryMax},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.last.String(), func(t *testing.T) {
			t.Parallel()
			mustMatch(t, nextRetry(c.last), c.next)
		})
	}
}

// An adapter with no physical address yet is left with a pending
// claim, which the kernel completes when the address arrives. The node
// workload joins on the kernel's event.
func TestAnAdapterJoinsWhenTheKernelCompletesItsClaim(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := fakeCECBus()
		adapter, device := wire.Adapter(cectest.Options{Physical: cec.InvalidPhysicalAddress, Capabilities: cec.CapLogAddrs | cec.CapTransmit})
		api.putDisplay("acm-0001-receiver", "node-1", "2.0.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		startNode(t, api, "node-1", device)
		entry := api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterJoining })
		mustMatch(t, entry.Message, "the adapter has no physical address yet, and the kernel claims a logical address only once it has one")

		adapter.Connect(0x2000)

		entry = api.scanned(t, "node-1")
		mustMatch(t, *entry.LogicalAddress, 4)
		mustMatch(t, entry.PhysicalAddress, "2.0.0.0")
		wire.Send(cec.GiveDevicePowerStatus(0, 4))
		api.waitUntil(t, "the answer to the TV", func() bool {
			for _, message := range wire.Sent() {
				if message.String() == cec.ReportPowerStatus(4, 0, cec.PowerStandby).String() {
					return true
				}
			}
			return false
		})
	})
}

// The node workload compares its entry with the stored one, so an
// entry that a restore or another writer removed or changed is written
// again while the bus is quiet.
func TestAStoredEntryThatDiffersIsWrittenAgain(t *testing.T) {
	cases := []struct {
		name   string
		change func(*cecAPI)
	}{
		{"removed", func(api *cecAPI) { api.dropEntry("den", "node-1") }},
		{"changed", func(api *cecAPI) {
			api.alterEntry("den", "node-1", func(entry *CECAdapterStatus) { entry.State = AdapterJoining })
		}},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := startCECAPI(t)
				wire := fakeCECBus()
				joinedNode(t, api, wire)
				sent := len(wire.Sent())

				c.change(api)

				api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
				mustMatch(t, len(wire.Sent()), sent)
			})
		})
	}
}
