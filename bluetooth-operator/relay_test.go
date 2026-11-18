package main

// These tests run the relay against the fake kernel in
// inputkernel_test.go, so they need no /dev/uinput and no real evdev
// node.

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
)

// waitFor polls condition until it returns true, and fails the test
// after 2 seconds. The relay copies events in its own goroutine, so a
// test cannot assume the copy has happened when a write returns.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("after 2 seconds, still waiting for %s", what)
}

// press returns one input_event record whose last byte is value.
func press(value byte) []byte {
	event := make([]byte, inputEventSize)
	event[inputEventSize-1] = value
	return event
}

const testMAC = "a0:ab:51:33:b7:12"

// relaysFor returns relays over a fake kernel that has every evdev node
// that the given HID devices register. A node's name comes from its HID
// device and its position under that device, so the relay treats a node
// that moves to another event number as the same device.
func relaysFor(t *testing.T, devices ...fakeHID) *relays {
	t.Helper()
	kernel := newFakeKernel()
	for _, device := range devices {
		for index, node := range realNodes(device) {
			kernel.register(node, device.Dir+" input"+strconv.Itoa(index))
		}
	}
	set := newRelays(kernel)
	t.Cleanup(func() { stopAll(set) })
	return set
}

// realNodes returns the evdev nodes of one fake HID device, as the
// sysfs walk returns them. The walk keeps only the DEVNAME values under
// input/event, so joydev's node is left out.
func realNodes(device fakeHID) []string {
	var nodes []string
	for _, node := range device.Nodes {
		if strings.HasPrefix(node, "input/event") {
			nodes = append(nodes, "/dev/"+node)
		}
	}
	return nodes
}

// stopAll stops every relay, so no goroutine keeps reading a node after
// the test ends.
func stopAll(set *relays) {
	set.mu.Lock()
	macs := slices.Collect(maps.Keys(set.controllers))
	set.mu.Unlock()
	for _, mac := range macs {
		set.stop(mac)
	}
}

// A DualSense registers a gamepad, a motion sensor, and a touchpad
// node. Each gets its own virtual device, so a consumer that opens the
// gamepad node does not receive the touchpad's events.
func TestEnsureCreatesOneVirtualDeviceForEachRealNode(t *testing.T) {
	kernel := newFakeKernel()
	kernel.register("/dev/input/event5", "Wireless Controller")
	kernel.register("/dev/input/event6", "Wireless Controller Motion Sensors")
	kernel.register("/dev/input/event7", "Wireless Controller Touchpad")
	relays := newRelays(kernel)
	t.Cleanup(func() { relays.stop(testMAC) })

	relays.ensure(testMAC, []string{"/dev/input/event5", "/dev/input/event6", "/dev/input/event7"})

	nodes := relays.virtualNodes(testMAC)
	if len(nodes) != 3 {
		t.Fatalf("the virtual nodes are %v, want three", nodes)
	}
	for _, device := range kernel.virtual {
		if device.phys != "bluetooth.liken.sh/a0:ab:51:33:b7:12" {
			t.Errorf("the virtual device has phys %q, want %q", device.phys, "bluetooth.liken.sh/a0:ab:51:33:b7:12")
		}
	}
}

// bluetooth_input_events_total counts the records the pump copies from
// a controller's real node to its virtual one. Its peripheral label is
// the Peripheral's resource name, not the colon-separated address that
// the other tests use.
func TestInputEventsAreCountedByPeripheral(t *testing.T) {
	kernel := newFakeKernel()
	kernel.register("/dev/input/event5", "Wireless Controller")
	relays := newRelays(kernel)
	relays.metrics = newMetrics()
	t.Cleanup(func() { relays.stop(testMAC) })
	relays.ensure(testMAC, []string{"/dev/input/event5"})

	if _, err := kernel.writer(t, "/dev/input/event5").Write(press(1)); err != nil {
		t.Fatalf("writing a press to the real node: %v", err)
	}
	if _, err := kernel.writer(t, "/dev/input/event5").Write(press(2)); err != nil {
		t.Fatalf("writing a press to the real node: %v", err)
	}

	waitFor(t, "both presses to be counted", func() bool {
		count, found := metricValue(t, relays.metrics.registry,
			"bluetooth_input_events_total", map[string]string{"peripheral": "a0-ab-51-33-b7-12"})
		return found && count == 2
	})
}

// Events written to the real node must arrive on the virtual node that
// the claim delivers.
func TestEventsFromARealNodeReachTheVirtualDevice(t *testing.T) {
	kernel := newFakeKernel()
	kernel.register("/dev/input/event5", "Wireless Controller")
	relays := newRelays(kernel)
	t.Cleanup(func() { relays.stop(testMAC) })
	relays.ensure(testMAC, []string{"/dev/input/event5"})

	if _, err := kernel.writer(t, "/dev/input/event5").Write(press(1)); err != nil {
		t.Fatalf("writing a press to the real node: %v", err)
	}

	virtual := kernel.virtual[0]
	waitFor(t, "the press to reach the virtual device", func() bool {
		return len(virtual.received()) == inputEventSize
	})
	if !reflect.DeepEqual(virtual.received(), press(1)) {
		t.Errorf("the virtual device received %v, want %v", virtual.received(), press(1))
	}
}

// A sleeping controller loses its real node, and the node returns at
// the next press, often with a different event number. The virtual node
// must stay the same, because the consumer's container keeps it open
// the whole time.
func TestARelaySurvivesTheRealNodeVanishingAndReturning(t *testing.T) {
	kernel := newFakeKernel()
	kernel.register("/dev/input/event5", "Wireless Controller")
	relays := newRelays(kernel)
	t.Cleanup(func() { relays.stop(testMAC) })
	relays.ensure(testMAC, []string{"/dev/input/event5"})
	before := relays.virtualNodes(testMAC)

	// The controller sleeps. The kernel removes the node, so the read
	// fails.
	if err := kernel.writer(t, "/dev/input/event5").CloseWithError(fmt.Errorf("no such device")); err != nil {
		t.Fatalf("closing the real node: %v", err)
	}
	// A pass with no node keeps the virtual device and opens nothing.
	relays.ensure(testMAC, nil)
	relays.ensure(testMAC, nil)
	if got := relays.virtualNodes(testMAC); !reflect.DeepEqual(got, before) {
		t.Fatalf("the virtual nodes are %v while the controller slept, want %v", got, before)
	}

	// The controller returns with a different event number.
	kernel.register("/dev/input/event9", "Wireless Controller")
	waitFor(t, "the relay to release the node that vanished", func() bool {
		relays.ensure(testMAC, []string{"/dev/input/event9"})
		return kernel.opened() == 2
	})
	if got := relays.virtualNodes(testMAC); !reflect.DeepEqual(got, before) {
		t.Fatalf("the virtual nodes are %v after the reconnect, want %v", got, before)
	}

	if _, err := kernel.writer(t, "/dev/input/event9").Write(press(1)); err != nil {
		t.Fatalf("writing a press to the real node: %v", err)
	}
	virtual := kernel.virtual[0]
	waitFor(t, "the press to reach the same virtual device", func() bool {
		return len(virtual.received()) == inputEventSize
	})
	if len(kernel.virtual) != 1 {
		t.Errorf("the reconnect created %d virtual devices, want one", len(kernel.virtual))
	}
}

// The snapshot in the bond's Secret is enough to create the virtual
// device before the controller connects. Without it, a claim on a
// sleeping controller would have no node to deliver.
func TestRestoreCreatesAVirtualDeviceWithNoRealNode(t *testing.T) {
	kernel := newFakeKernel()
	kernel.register("/dev/input/event5", "Wireless Controller")
	first := newRelays(kernel)
	first.ensure(testMAC, []string{"/dev/input/event5"})
	stored := first.snapshot(testMAC)
	first.stop(testMAC)

	// A new pod, where the controller is asleep and has no real node.
	restarted := newRelays(newFakeKernel())
	t.Cleanup(func() { restarted.stop(testMAC) })
	restarted.restore(testMAC, stored)

	nodes := restarted.virtualNodes(testMAC)
	if len(nodes) != 1 {
		t.Fatalf("the virtual nodes are %v, want one", nodes)
	}
	var document evdevSnapshot
	if err := json.Unmarshal(stored, &document); err != nil {
		t.Fatalf("reading the stored snapshot: %v", err)
	}
	if document.Version != snapshotVersion || len(document.Nodes) != 1 {
		t.Fatalf("the stored document is %+v, want version %d and one node", document, snapshotVersion)
	}
	if document.Nodes[0].Name != "Wireless Controller" {
		t.Errorf("the stored name is %q, want %q", document.Nodes[0].Name, "Wireless Controller")
	}
}

// A snapshot with an unknown version creates nothing. The next connect
// reads the real nodes and stores a snapshot with the current version.
func TestRestoreRefusesAnUnknownVersion(t *testing.T) {
	relays := newRelays(newFakeKernel())
	relays.restore(testMAC, []byte(`{"version":99,"nodes":[{"name":"Wireless Controller"}]}`))
	if nodes := relays.virtualNodes(testMAC); len(nodes) != 0 {
		t.Errorf("the virtual nodes are %v, want none", nodes)
	}
}

// An unpair closes the virtual device along with the bond. A claim must
// not deliver a node for a controller whose keys this machine no longer
// has.
func TestStopTakesTheVirtualDeviceAway(t *testing.T) {
	kernel := newFakeKernel()
	kernel.register("/dev/input/event5", "Wireless Controller")
	relays := newRelays(kernel)
	relays.ensure(testMAC, []string{"/dev/input/event5"})

	relays.stop(testMAC)

	if nodes := relays.virtualNodes(testMAC); len(nodes) != 0 {
		t.Errorf("the virtual nodes are %v, want none", nodes)
	}
	if !kernel.virtual[0].closed() {
		t.Error("the virtual device is open after stop, want it closed")
	}
	// An unpair repeats its steps after a failed pass, so stopping twice
	// must succeed too.
	relays.stop(testMAC)
}

// The sysfs walk that finds a controller's real nodes must never return
// a virtual node, because a relay that read its own virtual device
// would repeat every press.
func TestTheSysfsWalkNeverReturnsAVirtualNode(t *testing.T) {
	kernel := newFakeKernel()
	kernel.register("/dev/input/event5", "Wireless Controller")
	relays := newRelays(kernel)
	t.Cleanup(func() { relays.stop(testMAC) })
	relays.ensure(testMAC, []string{"/dev/input/event5"})

	// Virtual devices are under /sys/devices/virtual/input, and the walk
	// starts at /sys/bus/hid/devices.
	root := fakeSysfs(t, dualSense("0001", testMAC, "input/event5"))
	walked := nodesByMAC(discoverHIDDevices(root, bonds.Address{}))
	for _, virtual := range relays.virtualNodes(testMAC) {
		for _, node := range walked[testMAC] {
			if node == virtual {
				t.Errorf("the walk returned the virtual node %s, want only real nodes", virtual)
			}
		}
	}
}
