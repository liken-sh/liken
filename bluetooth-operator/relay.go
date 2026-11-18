package main

// The input relay. For each bonded controller, the operator creates one
// virtual input device (uinput) for each evdev node the controller
// registers, and copies events from the real node to it.
//
// A claim delivers the virtual node, not the real one. The kernel
// gives a controller a new event number each time it reconnects, but a
// virtual node keeps its number for as long as the operator keeps the
// uinput fd open. So the node in a consumer's container stays valid
// while the controller sleeps and after it reconnects on another event
// number.
//
// The virtual devices exist whether or not a claim names the
// controller. With no prepared claim on the controller, the relay
// copies nothing. Otherwise each node passes the event types of the
// input classes that the claims request (claimsettings.go, inputMasks
// in inputs.go).
//
// The relay matches a real node to its virtual device by the name the
// kernel reports for it (EVIOCGNAME), so each node of a controller with
// several, such as a DualSense, returns to its own virtual device after
// a reconnect. Events go in one direction only. A gamepad's rumble is a
// write to the real node, and the relay does not copy it back.
//
// When the operator restarts, the kernel closes every uinput fd, and
// restore creates the virtual devices again from the stored snapshots.
// The kernel gives the new devices free minor numbers, which can be
// numbers that a consumer received for a different controller. The
// node-moved taint (devices.go) evicts a consumer whose prepared claim
// has nodes that the relay does not deliver now.

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
)

// relays has the relay of every controller. The reconcile loop calls
// ensure, and the DRA plugin calls virtualNodes from the goroutine that
// serves the kubelet, so mu guards the whole structure.
type relays struct {
	kernel inputKernel

	// metrics and recorder can be nil, and then nothing is recorded or
	// posted.
	metrics  *metrics
	recorder *events.Recorder

	// now measures how long a remote slept, for the wake press
	// (wake.go). Tests replace it.
	now func() time.Time

	mu          sync.Mutex
	controllers map[string]*controllerRelay

	// createFailed has the controllers whose last attempt to create a
	// virtual device failed. Each pass tries again, so create posts the
	// InputRelayFailed Event only when a controller enters this set.
	createFailed map[string]bool
}

func newRelays(kernel inputKernel) *relays {
	return &relays{kernel: kernel, now: time.Now, controllers: map[string]*controllerRelay{}, createFailed: map[string]bool{}}
}

// controllerRelay is the relay of one controller. nodes has its virtual
// devices, keyed by the name the real node reports. claims has the
// input settings of each prepared claim on the controller, keyed by
// the claim's UID.
//
// asleepSince is the time the controller's link went down with the
// reason Asleep, and it is zero when no wake press is due.
// wakePressedFor is the asleepSince of the last wake press, so that
// follow does not schedule a press for the same sleep again (wake.go).
type controllerRelay struct {
	nodes  map[string]*nodeRelay
	claims map[string]inputSettings

	asleepSince    time.Time
	wakePressedFor time.Time
}

// nodeRelay is one virtual device and the real node its events come
// from. source is nil while the controller is disconnected, which is
// the normal state of a Low Energy remote between presses. masked is
// true while an event mask is set on source. applyMask reads it to
// clear the mask when the claims request every event again.
type nodeRelay struct {
	caps       evdevCapabilities
	device     virtualDevice
	source     realNode
	sourcePath string
	masked     bool
}

func (c *controllerRelay) isReading(path string) bool {
	for _, relay := range c.nodes {
		if relay.sourcePath == path {
			return true
		}
	}
	return false
}

// ensure starts to copy events from each real node in nodes that the
// relay does not read yet. nodes is empty for a controller that is
// asleep or switched off, and then the virtual devices stay as they
// are.
//
// The reconcile loop calls ensure on every pass, and publisher.attach
// calls it on each uevent without waiting for the settle window. The
// kernel sends an input uevent when a node appears, so nothing polls
// for a node.
func (r *relays) ensure(mac string, nodes []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	controller := r.controller(mac)

	// Retry a virtual device that could not be created earlier, for
	// example when restore ran before /dev/uinput existed. Without
	// this, the device would not exist until the next reconnect.
	for _, relay := range controller.nodes {
		if relay.device == nil {
			r.create(mac, relay)
		}
	}

	for _, path := range nodes {
		if controller.isReading(path) {
			continue
		}
		caps, err := r.kernel.readCapabilities(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "relay: reading %s of controller %s: %v\n", path, publishedMAC(mac), err)
			continue
		}
		relay := controller.nodes[caps.Name]
		if relay == nil {
			relay = &nodeRelay{caps: caps}
			controller.nodes[caps.Name] = relay
		}
		// Store what the controller reports now, but do not rebuild the
		// existing virtual device, because that would remove the node
		// from a container that has it open. The next start of this pod
		// creates the device from the new snapshot.
		//
		// For an axis that this operator tunes, the kernel reports the
		// fuzz and flat that the operator wrote. If the snapshot stored
		// them, the device's own values would be lost, so snapshotAxes
		// keeps the stored values for those axes.
		caps.Axes = snapshotAxes(relay.caps.Axes, caps.Axes, controller.combinedSettings().axes)
		relay.caps = caps
		if relay.device == nil && !r.create(mac, relay) {
			continue
		}
		if relay.source != nil {
			fmt.Fprintf(os.Stderr, "relay: controller %s has a second node named %q at %s, and the relay does not copy it\n",
				publishedMAC(mac), caps.Name, path)
			continue
		}
		r.read(mac, controller, relay, path)
	}
}

// controller returns the relay for mac, and creates an empty one when
// there is none. The caller holds mu.
func (r *relays) controller(mac string) *controllerRelay {
	controller := r.controllers[mac]
	if controller == nil {
		controller = &controllerRelay{
			nodes:  map[string]*nodeRelay{},
			claims: map[string]inputSettings{},
		}
		r.controllers[mac] = controller
	}
	return controller
}

// restore creates a controller's virtual devices from the snapshot in
// its bond's Secret when this pod starts. Without it, a claim on a
// sleeping controller has no node to deliver until someone presses a
// button.
func (r *relays) restore(mac string, stored []byte) {
	if len(stored) == 0 {
		return
	}
	var document evdevSnapshot
	if err := json.Unmarshal(stored, &document); err != nil {
		fmt.Fprintf(os.Stderr, "relay: reading the stored capabilities of controller %s: %v\n", publishedMAC(mac), err)
		return
	}
	if document.Version != snapshotVersion {
		fmt.Fprintf(os.Stderr, "relay: the stored capabilities of controller %s have version %d, and this operator reads version %d\n",
			publishedMAC(mac), document.Version, snapshotVersion)
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	controller := r.controller(mac)
	for _, caps := range document.Nodes {
		if _, found := controller.nodes[caps.Name]; found {
			continue
		}
		relay := &nodeRelay{caps: caps}
		controller.nodes[caps.Name] = relay
		r.create(mac, relay)
	}
}

// snapshot returns the JSON that the bond's Secret stores under the
// evdev key: the capabilities of each of the controller's nodes. It
// returns nil for a controller that has never connected.
func (r *relays) snapshot(mac string) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	controller := r.controllers[mac]
	if controller == nil || len(controller.nodes) == 0 {
		return nil
	}
	document := evdevSnapshot{Version: snapshotVersion}
	for _, relay := range controller.nodes {
		document.Nodes = append(document.Nodes, relay.caps)
	}
	// The caller compares the document with the stored one byte for
	// byte, so the order must not depend on map iteration.
	slices.SortFunc(document.Nodes, func(a, b evdevCapabilities) int {
		return strings.Compare(a.Name, b.Name)
	})
	raw, err := json.Marshal(document)
	if err != nil {
		fmt.Fprintf(os.Stderr, "relay: encoding the capabilities of controller %s: %v\n", publishedMAC(mac), err)
		return nil
	}
	return raw
}

// virtualNodes returns the sorted paths of the virtual nodes that a
// claim on this controller delivers. It returns none for a bond that
// has never connected, or when the kernel refused to create each of its
// virtual devices, and the no-input-node taint (devices.go) reports it.
func (r *relays) virtualNodes(mac string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	controller := r.controllers[mac]
	if controller == nil {
		return nil
	}
	var nodes []string
	for _, relay := range controller.nodes {
		if relay.device != nil {
			nodes = append(nodes, relay.device.node())
		}
	}
	slices.Sort(nodes)
	return nodes
}

// classes returns the union of the input classes that udev would set on
// each of the controller's nodes. It reads the stored capabilities, so
// a sleeping controller that has connected once still reports them.
func (r *relays) classes(mac string) inputClasses {
	r.mu.Lock()
	defer r.mu.Unlock()
	controller := r.controllers[mac]
	if controller == nil {
		return 0
	}
	var carried inputClasses
	for _, relay := range controller.nodes {
		carried |= nodeInputClasses(relay.caps)
	}
	return carried
}

// stop closes a controller's real and virtual nodes and deletes its
// relay. An unpair calls it after the claim on the controller is
// released, and repeats its steps after a failed pass, so stop does
// nothing for a controller with no relay.
func (r *relays) stop(mac string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	controller := r.controllers[mac]
	if controller == nil {
		return
	}
	delete(r.controllers, mac)
	delete(r.createFailed, mac)
	for _, relay := range controller.nodes {
		if relay.source != nil {
			_ = relay.source.Close()
		}
		if relay.device != nil {
			_ = relay.device.close()
		}
	}
	fmt.Printf("relay: removed the virtual input devices of controller %s\n", publishedMAC(mac))
}

// create creates the virtual device for relay and keeps it open. It
// returns false when the kernel refuses. The caller holds mu.
func (r *relays) create(mac string, relay *nodeRelay) bool {
	device, err := r.kernel.createVirtual(relay.caps, relayPhys(mac))
	if err != nil {
		fmt.Fprintf(os.Stderr, "relay: creating a virtual device for controller %s: %v\n", publishedMAC(mac), err)
		if !r.createFailed[mac] {
			r.createFailed[mac] = true
			r.recorder.Warning(controllerReference(mac), reasonInputRelayFailed,
				fmt.Sprintf("The operator cannot create the virtual input device of %s: %v", publishedMAC(mac), err))
		}
		return false
	}
	delete(r.createFailed, mac)
	relay.device = device
	if !withinContainerNodes(device.node()) {
		// The adapter claim creates event0 through event31 in this
		// container, so a higher node exists only on the host. A claim
		// that delivered it would fail container creation, because the
		// runtime creates the device node from the major and minor
		// numbers of a node it can read.
		fmt.Fprintf(os.Stderr, "relay: the virtual device of controller %s is %s, above the %d event nodes that the pod has, so a claim cannot deliver it\n",
			publishedMAC(mac), device.node(), containerInputNodes)
		r.recorder.Warning(controllerReference(mac), reasonInputRelayFailed,
			fmt.Sprintf("The virtual input device of %s is %s, above the %d event nodes that the pod has, so a claim on it cannot be prepared",
				publishedMAC(mac), device.node(), containerInputNodes))
	}
	fmt.Printf("relay: created the virtual input device %s of controller %s, named %q\n", device.node(), publishedMAC(mac), relay.caps.Name)
	return true
}

// read opens the real node at path and starts copying its events. First
// it sets the event mask and the axis values that the controller's
// prepared claims request, because a reconnect creates a new kernel
// device that has neither. It writes a due wake press before the pump
// starts, so the press arrives before any real event. The caller holds
// mu.
func (r *relays) read(mac string, controller *controllerRelay, relay *nodeRelay, path string) {
	want := controller.combinedSettings()
	source, err := r.kernel.open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "relay: opening %s of controller %s: %v\n", path, publishedMAC(mac), err)
		return
	}
	relay.source, relay.sourcePath, relay.masked = source, path, false
	relay.applyMask(mac, want.classes)
	relay.tune(mac, want.axes)
	// The kernel does not queue events for a node that nobody has
	// opened, so a press before this open is lost. This line lets a
	// person compare the time of the open with the time of the input
	// uevent.
	fmt.Printf("relay: reading %s of controller %s into %s\n", path, publishedMAC(mac), relay.device.node())
	r.pressToWake(mac, controller, relay, want.classes)
	go r.pump(mac, relay, source)
}

// pump copies events from a real node to its virtual device until a
// read or a write fails. A read fails when the controller disconnects
// and the kernel removes the node. pump then closes the real node and
// clears source, so the next ensure that lists a node opens it again.
//
// When no prepared claim requests anything from the node, the event mask
// blocks every type, and the kernel also drops the SYN_REPORT of the
// empty frame, so pump stays blocked in the read and uses no CPU.
//
// pump does not decode events. Writing the bytes of input_event records
// to the uinput fd produces the same events on the virtual device. A
// read can end in the middle of a record, so pump keeps the partial
// record for the next read.
func (r *relays) pump(mac string, relay *nodeRelay, source realNode) {
	buffer := make([]byte, inputEventSize*64)
	partial := 0
	for {
		read, err := source.Read(buffer[partial:])
		partial += read
		if whole := partial - partial%inputEventSize; whole > 0 {
			if writeErr := relay.device.write(buffer[:whole]); writeErr != nil {
				fmt.Fprintf(os.Stderr, "relay: writing to %s: %v\n", relay.device.node(), writeErr)
				break
			}
			r.metrics.countInputEvents(deviceName(mac), whole/inputEventSize)
			partial = copy(buffer, buffer[whole:partial])
		}
		if err != nil {
			break
		}
	}
	_ = source.Close()

	r.mu.Lock()
	defer r.mu.Unlock()
	if relay.source == source {
		relay.source, relay.sourcePath = nil, ""
	}
}

// relayPhys returns the phys string of a controller's virtual devices,
// driver/address. It appears in /proc/bus/input/devices, so a person
// can see which controller a virtual device copies.
func relayPhys(mac string) string {
	return DriverName + "/" + normalizeMAC(mac)
}

// snapshotAxes returns the axes to store for a node: read, except that
// an axis in tuned keeps the fuzz and flat of stored.
func snapshotAxes(stored, read []absAxis, tuned axisOverrides) []absAxis {
	if len(tuned) == 0 {
		return read
	}
	own := make(map[uint16]absAxis, len(stored))
	for _, axis := range stored {
		own[axis.Code] = axis
	}
	kept := make([]absAxis, 0, len(read))
	for _, axis := range read {
		if _, overridden := tuned[axis.Code]; overridden {
			if before, found := own[axis.Code]; found {
				axis.Fuzz, axis.Flat = before.Fuzz, before.Flat
			}
		}
		kept = append(kept, axis)
	}
	return kept
}
