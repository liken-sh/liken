package main

// These tests drive the relay's policy with no kernel. The fake kernel
// answers with capabilities the test chose, hands out an io.Pipe for
// each real node, and records the virtual devices the relay created and
// what reached them. What the tests assert is what a consumer of a
// claim sees: which node the claim delivers, that it stays the same
// node across a reconnect, and that presses on the real node arrive on
// it.

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// fakeKernel stands in for /dev/uinput and the real evdev nodes.
type fakeKernel struct {
	mu sync.Mutex

	// capabilities answers readCapabilities for each real node path.
	capabilities map[string]evdevCapabilities

	// writers holds the write end of each opened real node, so a test
	// can send events into the relay and can end the read.
	writers map[string]*io.PipeWriter

	// virtual is every device the relay created, newest last.
	virtual []*fakeVirtual

	// nextNode numbers the nodes this kernel hands out.
	nextNode int
}

func newFakeKernel() *fakeKernel {
	return &fakeKernel{
		capabilities: map[string]evdevCapabilities{},
		writers:      map[string]*io.PipeWriter{},
	}
}

// register declares what a real node reports for itself.
func (k *fakeKernel) register(path, name string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.capabilities[path] = evdevCapabilities{
		Name:  name,
		ID:    evdevID{Bus: 0x0005, Vendor: 0x054c, Product: 0x0ce6},
		Codes: map[string][]uint16{"EV_KEY": {0x130}},
	}
}

func (k *fakeKernel) readCapabilities(path string) (evdevCapabilities, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	caps, found := k.capabilities[path]
	if !found {
		return evdevCapabilities{}, fmt.Errorf("no such device %s", path)
	}
	return caps, nil
}

func (k *fakeKernel) open(path string) (io.ReadCloser, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, found := k.capabilities[path]; !found {
		return nil, fmt.Errorf("no such device %s", path)
	}
	reader, writer := io.Pipe()
	k.writers[path] = writer
	return reader, nil
}

func (k *fakeKernel) createVirtual(caps evdevCapabilities, phys string) (virtualDevice, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	device := &fakeVirtual{path: fmt.Sprintf("/dev/input/event%d", k.nextNode), phys: phys, caps: caps}
	k.nextNode++
	k.virtual = append(k.virtual, device)
	return device, nil
}

// opened is how many real nodes the relay has opened so far.
func (k *fakeKernel) opened() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.writers)
}

// writer answers with the write end of one opened real node.
func (k *fakeKernel) writer(t *testing.T, path string) *io.PipeWriter {
	t.Helper()
	k.mu.Lock()
	defer k.mu.Unlock()
	writer, found := k.writers[path]
	if !found {
		t.Fatalf("the relay never opened %s", path)
	}
	return writer
}

// fakeVirtual is one virtual device, and the events that reached it.
type fakeVirtual struct {
	path  string
	phys  string
	caps  evdevCapabilities
	mu    sync.Mutex
	got   []byte
	ended bool
}

func (d *fakeVirtual) node() string { return d.path }

func (d *fakeVirtual) write(events []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ended {
		return fmt.Errorf("%s is closed", d.path)
	}
	d.got = append(d.got, events...)
	return nil
}

func (d *fakeVirtual) close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ended = true
	return nil
}

// closed reports whether the relay destroyed this device.
func (d *fakeVirtual) closed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ended
}

func (d *fakeVirtual) received() []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]byte(nil), d.got...)
}

// waitFor runs a condition until it holds or the deadline passes. The
// relay pumps events from its own goroutine, so a test waits for the
// result instead of assuming the goroutine has run.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// press is one event record, as the kernel lays it out.
func press(value byte) []byte {
	event := make([]byte, inputEventSize)
	event[inputEventSize-1] = value
	return event
}

const testMAC = "a0:ab:51:33:b7:12"

// relaysFor is a relay desk over a fake kernel that answers for every
// evdev node these HID devices register. The tests of the pass need
// each connected controller to have a virtual node, and the name a
// node reports is derived from the HID device and the node's position
// under it, so a node that moves to another number is still the same
// device to the relay.
func relaysFor(t *testing.T, devices ...fakeHID) *relays {
	t.Helper()
	kernel := newFakeKernel()
	for _, device := range devices {
		for index, node := range realNodes(device) {
			kernel.register(node, device.Dir+" input"+strconv.Itoa(index))
		}
	}
	held := newRelays(kernel)
	t.Cleanup(func() { stopAll(held) })
	return held
}

// realNodes are the evdev nodes one fake HID device registers, as the
// sysfs walk answers with them. joydev's own node is not one: the walk
// keeps the DEVNAME values under input/event and nothing else.
func realNodes(device fakeHID) []string {
	var nodes []string
	for _, node := range device.Nodes {
		if strings.HasPrefix(node, "input/event") {
			nodes = append(nodes, "/dev/"+node)
		}
	}
	return nodes
}

// stopAll ends every relay, so a test leaves no goroutine reading a
// node that no longer matters.
func stopAll(held *relays) {
	held.mu.Lock()
	macs := slices.Collect(maps.Keys(held.held))
	held.mu.Unlock()
	for _, mac := range macs {
		held.stop(mac)
	}
}

// A DualSense registers three evdev nodes, and each one becomes its
// own virtual device, because a consumer that opens the gamepad node
// must not receive the touchpad's events.
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
		t.Fatalf("virtual nodes = %v, want three", nodes)
	}
	// The physical address names this operator and the controller, so a
	// person reading the virtual device knows where its events come
	// from.
	for _, device := range kernel.virtual {
		if device.phys != "bluetooth.liken.sh/a0:ab:51:33:b7:12" {
			t.Errorf("phys = %q", device.phys)
		}
	}
}

// The relay is the whole delivery: what the controller sends must
// arrive on the node the claim delivered.
func TestEventsFromARealNodeReachTheVirtualDevice(t *testing.T) {
	kernel := newFakeKernel()
	kernel.register("/dev/input/event5", "Wireless Controller")
	relays := newRelays(kernel)
	t.Cleanup(func() { relays.stop(testMAC) })
	relays.ensure(testMAC, []string{"/dev/input/event5"})

	if _, err := kernel.writer(t, "/dev/input/event5").Write(press(1)); err != nil {
		t.Fatal(err)
	}

	virtual := kernel.virtual[0]
	waitFor(t, "the press to reach the virtual device", func() bool {
		return len(virtual.received()) == inputEventSize
	})
	if !reflect.DeepEqual(virtual.received(), press(1)) {
		t.Errorf("the virtual device received %v", virtual.received())
	}
}

// A controller that sleeps takes its real node away and brings it back
// on the next press, often as a different event number. The virtual
// node must be the same node before and after, because the consumer's
// container holds it open the whole time.
func TestARelaySurvivesTheRealNodeVanishingAndReturning(t *testing.T) {
	kernel := newFakeKernel()
	kernel.register("/dev/input/event5", "Wireless Controller")
	relays := newRelays(kernel)
	t.Cleanup(func() { relays.stop(testMAC) })
	relays.ensure(testMAC, []string{"/dev/input/event5"})
	before := relays.virtualNodes(testMAC)

	// The controller slept. The kernel removed the node, and the read
	// on it fails.
	if err := kernel.writer(t, "/dev/input/event5").CloseWithError(fmt.Errorf("no such device")); err != nil {
		t.Fatal(err)
	}
	// Two passes with no node: the virtual device stays, and the relay
	// does not open anything.
	relays.ensure(testMAC, nil)
	relays.ensure(testMAC, nil)
	if got := relays.virtualNodes(testMAC); !reflect.DeepEqual(got, before) {
		t.Fatalf("virtual nodes = %v while the controller slept, want %v", got, before)
	}

	// The controller returned on a different event number.
	kernel.register("/dev/input/event9", "Wireless Controller")
	waitFor(t, "the relay to release the node that vanished", func() bool {
		relays.ensure(testMAC, []string{"/dev/input/event9"})
		return kernel.opened() == 2
	})
	if got := relays.virtualNodes(testMAC); !reflect.DeepEqual(got, before) {
		t.Fatalf("virtual nodes = %v after the reconnect, want %v", got, before)
	}

	if _, err := kernel.writer(t, "/dev/input/event9").Write(press(1)); err != nil {
		t.Fatal(err)
	}
	virtual := kernel.virtual[0]
	waitFor(t, "the press to reach the same virtual device", func() bool {
		return len(virtual.received()) == inputEventSize
	})
	if len(kernel.virtual) != 1 {
		t.Errorf("the reconnect created %d virtual devices, want one", len(kernel.virtual))
	}
}

// The snapshot is what the bond's Secret holds, and it is enough to
// create the virtual device before the controller has connected on
// this boot. Without it, a claim on a controller that is asleep would
// have nothing to deliver.
func TestRestoreCreatesAVirtualDeviceWithNoRealNode(t *testing.T) {
	kernel := newFakeKernel()
	kernel.register("/dev/input/event5", "Wireless Controller")
	first := newRelays(kernel)
	first.ensure(testMAC, []string{"/dev/input/event5"})
	stored := first.snapshot(testMAC)
	first.stop(testMAC)

	// A new pod, with the controller asleep and no real node anywhere.
	restarted := newRelays(newFakeKernel())
	t.Cleanup(func() { restarted.stop(testMAC) })
	restarted.restore(testMAC, stored)

	nodes := restarted.virtualNodes(testMAC)
	if len(nodes) != 1 {
		t.Fatalf("virtual nodes = %v, want one", nodes)
	}
	var document evdevSnapshot
	if err := json.Unmarshal(stored, &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != snapshotVersion || len(document.Nodes) != 1 {
		t.Fatalf("the stored document = %+v", document)
	}
	if document.Nodes[0].Name != "Wireless Controller" {
		t.Errorf("the stored name = %q", document.Nodes[0].Name)
	}
}

// A snapshot from a version this operator does not recognize creates
// nothing. The next connect reads the real nodes and writes a document
// this operator does understand.
func TestRestoreRefusesAnUnknownVersion(t *testing.T) {
	relays := newRelays(newFakeKernel())
	relays.restore(testMAC, []byte(`{"version":99,"nodes":[{"name":"Wireless Controller"}]}`))
	if nodes := relays.virtualNodes(testMAC); len(nodes) != 0 {
		t.Errorf("virtual nodes = %v, want none", nodes)
	}
}

// An unpair takes the virtual device with the bond. Nothing may
// deliver a node for a controller this machine no longer holds keys
// for.
func TestStopTakesTheVirtualDeviceAway(t *testing.T) {
	kernel := newFakeKernel()
	kernel.register("/dev/input/event5", "Wireless Controller")
	relays := newRelays(kernel)
	relays.ensure(testMAC, []string{"/dev/input/event5"})

	relays.stop(testMAC)

	if nodes := relays.virtualNodes(testMAC); len(nodes) != 0 {
		t.Errorf("virtual nodes = %v, want none", nodes)
	}
	if !kernel.virtual[0].closed() {
		t.Error("the virtual device is still open")
	}
	// A teardown repeats a step whenever a pass fails, so stopping
	// twice must answer the same way.
	relays.stop(testMAC)
}

// The relay's own devices are not on the HID bus, so the walk that
// finds a controller's real nodes never finds a virtual one. A relay
// that read its own output would repeat every press.
func TestTheSysfsWalkNeverReturnsAVirtualNode(t *testing.T) {
	kernel := newFakeKernel()
	kernel.register("/dev/input/event5", "Wireless Controller")
	relays := newRelays(kernel)
	t.Cleanup(func() { relays.stop(testMAC) })
	relays.ensure(testMAC, []string{"/dev/input/event5"})

	// The virtual device is under /sys/devices/virtual/input, and the
	// walk starts at /sys/bus/hid/devices.
	root := fakeSysfs(t, dualSense("0001", testMAC, "input/event5"))
	walked := nodesByMAC(discoverHIDDevices(root, bonds.Address{}))
	for _, virtual := range relays.virtualNodes(testMAC) {
		for _, node := range walked[testMAC] {
			if node == virtual {
				t.Errorf("the walk returned the virtual node %s", virtual)
			}
		}
	}
}
