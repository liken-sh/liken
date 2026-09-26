package main

// The node workload's life around the adapter: the steady report, the
// release when it stops, the release when no bus names its machine,
// and the retries. cecnode_test.go holds the fixtures.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

// shorten sets a duration for one test and restores it after the node
// workloads the test started have stopped, because t.Cleanup runs the
// last registered function first.
func shorten(t *testing.T, setting *time.Duration, value time.Duration) {
	t.Helper()
	was := *setting
	*setting = value
	t.Cleanup(func() { *setting = was })
}

// joinedNode is a node workload in Control on the room's wire that has
// finished a scan.
func joinedNode(t *testing.T, api *cecAPI, wire *cectest.Bus) (*cectest.Adapter, context.CancelFunc, <-chan error) {
	t.Helper()
	adapter, device := usbAdapter(wire)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	node, err := newCECNode(api.client, "node-1", device)
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
		case <-time.After(testTimeout):
			t.Error("the node workload did not stop")
		}
	})
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	return adapter, cancel, done
}

func TestTheEntryIsWrittenOnASteadyInterval(t *testing.T) {
	shorten(t, &cecReportInterval, 20*time.Millisecond)
	api := startCECAPI(t)
	joinedNode(t, api, cecRoom())
	before := api.writesOf("node-1")

	api.waitUntil(t, "three more writes with nothing changed", func() bool { return api.writesOf("node-1") >= before+3 })

	entry, _ := api.entry("den", "node-1")
	if _, err := time.Parse(time.RFC3339, entry.ReportedAt); err != nil {
		t.Errorf("reportedAt = %q: %v", entry.ReportedAt, err)
	}
}

// A pod that stops takes the adapter off the bus, and its entry says
// so, because a claim the kernel kept would leave the adapter answering
// the TV for a machine no pod serves.
func TestAStoppedNodeReleasesTheAdapter(t *testing.T) {
	api := startCECAPI(t)
	adapter, cancel, done := joinedNode(t, api, cecRoom())

	cancel()

	mustSucceed(t, waitForExit(t, done))
	if held := adapter.Logical(); len(held) != 0 || adapter.Follows() {
		t.Errorf("a stopped node leaves the adapter holding %v, following %v", held, adapter.Follows())
	}
	entry, _ := api.entry("den", "node-1")
	mustMatch(t, entry.State, AdapterStopped)
	if entry.LogicalAddress != nil || !strings.HasPrefix(entry.Message, "the node workload stopped: context canceled") {
		t.Errorf("entry = %+v", entry)
	}
}

func TestAnUnpluggedAdapterLeavesAStoppedEntry(t *testing.T) {
	api := startCECAPI(t)
	adapter, _, done := joinedNode(t, api, cecRoom())

	adapter.Unplug()

	if err := waitForExit(t, done); !cec.IsGone(err) {
		t.Errorf("ended with %v, want ENODEV", err)
	}
	entry, _ := api.entry("den", "node-1")
	mustMatch(t, entry.State, AdapterStopped)
	if !strings.HasPrefix(entry.Message, "the adapter failed: ") || !strings.Contains(entry.Message, "no such device") {
		t.Errorf("message = %q", entry.Message)
	}
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

// When no bus names the machine and the node workload cannot make one,
// the adapter leaves the bus it was on and its entry goes.
func TestAnAdapterNoBusNamesLeavesTheBus(t *testing.T) {
	api := startCECAPI(t)
	adapter, _, _ := joinedNode(t, api, cecRoom())

	api.putBus(CECBus{Metadata: ObjectMeta{Name: "node-1"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-2"}}}})
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-2", Display: "acm-0001-receiver"}))

	api.waitUntil(t, "the adapter to leave the bus", func() bool {
		_, held := api.entry("den", "node-1")
		return !held && len(adapter.Logical()) == 0 && !adapter.Follows()
	})
}

// A Display that cannot be read is not a new address: the adapter
// keeps the address it announces, stays joined, and says why.
func TestAFailedDisplayReadKeepsTheAdapterJoined(t *testing.T) {
	api := startCECAPI(t)
	wire := cecRoom()
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
}

// A join that failed is tried again on a later pass, with no change to
// the spec.
func TestAFailedJoinIsTriedAgain(t *testing.T) {
	shorten(t, &cecRetryFirst, time.Millisecond)
	api := startCECAPI(t)
	wire := cecRoom()
	for _, address := range []cec.LogicalAddress{4, 8, 11} {
		wire.Add(cectest.Peer{Logical: address, Physical: 0x2000, PrimaryType: 4})
	}
	_, device := usbAdapter(wire)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	startNode(t, api, "node-1", device)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterJoining })

	wire.Remove(8)
	time.Sleep(5 * time.Millisecond)
	api.nudge()

	entry := api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	mustMatch(t, *entry.LogicalAddress, 8)
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
	for _, c := range cases {
		t.Run(c.last.String(), func(t *testing.T) {
			mustMatch(t, nextRetry(c.last), c.next)
		})
	}
}

// An adapter on a video port with no address yet stores its claim, and
// the kernel completes the claim when the address arrives. The node
// workload follows the kernel's event, joins, and answers the TV.
func TestAClaimTheKernelCompletesLaterJoins(t *testing.T) {
	api := startCECAPI(t)
	wire := cecRoom()
	adapter, device := wire.Adapter(cectest.Options{Physical: cec.InvalidPhysicalAddress, Capabilities: cec.CapLogAddrs | cec.CapTransmit})
	api.putDisplay("acm-0001-receiver", "node-1", "2.0.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	startNode(t, api, "node-1", device)
	entry := api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterJoining })
	mustMatch(t, entry.Message, "the adapter has no physical address yet; the kernel claims a logical address once it has one")

	adapter.Connect(0x2000)

	entry = api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	mustMatch(t, *entry.LogicalAddress, 4)
	mustMatch(t, entry.PhysicalAddress, "2.0.0.0")
	wire.Send(cec.GiveDevicePowerStatus(0, 4))
	api.waitUntil(t, "the answer to the TV", func() bool {
		for _, message := range wire.Sent() {
			if message.String() == cec.ReportPowerStatus(4, 0, cec.PowerOn).String() {
				return true
			}
		}
		return false
	})
}
