package main

// The input relay, which is what a claim on a controller delivers.
//
// For each bonded controller the operator holds one virtual input
// device open for each evdev node the controller registers, whether or
// not any claim names the controller. The virtual node's number is
// fixed for as long as the operator holds the uinput fd open, so the
// node a consumer's container received at start is still there when
// the controller sleeps and returns on a different event number. A
// sleeping controller is a virtual device that emits nothing.
//
// Which events cross is the other half, in demand.go. A node's events
// cross only while a prepared claim asks for a class the node carries.
// So a virtual device exists for every node, and it carries only what
// somebody claimed.
//
// A real node is matched to its virtual device by the name the kernel
// reports for it (EVIOCGNAME), so a reconnect maps each of a DualSense's
// three nodes back to the same virtual one. Events go one way: a
// gamepad's rumble is a write into the real node, and nothing here
// carries it back.
//
// A restart of this operator closes every uinput fd, and restore
// creates the virtual devices again from the stored snapshots. The
// kernel numbers them from the free minors, which then include the
// numbers the controllers' own nodes held, so a virtual device can
// land on a number a consumer received for a different controller. The
// node-moved taint in slices.go is the repair: it evicts the consumer
// whose prepared claim names nodes the relay no longer delivers.

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
)

// relays holds every controller's relay. The reconcile loop calls
// ensure, and the DRA plugin calls virtualNodes from the goroutine
// that answers the kubelet, so one lock covers the whole structure.
type relays struct {
	kernel inputKernel

	mu   sync.Mutex
	held map[string]*controllerRelay
}

func newRelays(kernel inputKernel) *relays {
	return &relays{kernel: kernel, held: map[string]*controllerRelay{}}
}

// controllerRelay is one controller's virtual devices, keyed by the
// name the real node reports for itself, and what each prepared claim
// on the controller asks those devices to deliver, keyed by the
// claim's UID.
type controllerRelay struct {
	nodes  map[string]*nodeRelay
	demand map[string]inputClasses
}

// nodeRelay is one virtual device and the real node its events come
// from. source is nil while the controller is off the air, which is
// the resting state of a Low Energy remote. narrowed says whether the
// kernel holds a mask on the open fd, which decides whether widening
// the demand has anything to undo.
type nodeRelay struct {
	caps       evdevCapabilities
	device     virtualDevice
	source     realNode
	sourcePath string
	narrowed   bool
}

// reads reports whether one of this controller's relays already moves
// events from a node.
func (c *controllerRelay) reads(path string) bool {
	for _, relay := range c.nodes {
		if relay.sourcePath == path {
			return true
		}
	}
	return false
}

// ensure makes one controller's relay agree with the evdev nodes the
// controller registers right now. nodes is empty for a controller that
// is asleep or switched off, and that is an ordinary pass: the virtual
// devices stay.
//
// The reconcile loop calls ensure on every pass, and the kernel's
// uevents already wake the loop when a node appears, so nothing here
// polls for a node that is not there.
func (r *relays) ensure(mac string, nodes []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	held := r.controller(mac)

	// A virtual device that could not be created on an earlier pass is
	// created here, so a snapshot that restored before /dev/uinput
	// answered is not held back until the next reconnect.
	for _, relay := range held.nodes {
		if relay.device == nil {
			r.create(mac, relay)
		}
	}

	for _, path := range nodes {
		if held.reads(path) {
			continue
		}
		caps, err := r.kernel.readCapabilities(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "relay: reading %s of controller %s: %v\n", path, publishedMAC(mac), err)
			continue
		}
		relay := held.nodes[caps.Name]
		if relay == nil {
			relay = &nodeRelay{caps: caps}
			held.nodes[caps.Name] = relay
		}
		// The recorded capabilities follow the real node, so the stored
		// snapshot states what the controller reports now. The virtual device
		// is not rebuilt to match, because rebuilding it takes the node away
		// from a container that holds it open. The next start of this pod
		// creates the device from the new snapshot.
		relay.caps = caps
		if relay.device == nil && !r.create(mac, relay) {
			continue
		}
		if relay.source != nil {
			fmt.Fprintf(os.Stderr, "relay: controller %s registers a second node named %q at %s, which is not relayed\n",
				publishedMAC(mac), caps.Name, path)
			continue
		}
		r.read(mac, relay, path, held.demanded())
	}
}

// controller is one controller's relay, created empty the first time
// anything names the controller. The caller holds the lock.
func (r *relays) controller(mac string) *controllerRelay {
	held := r.held[mac]
	if held == nil {
		held = &controllerRelay{
			nodes:  map[string]*nodeRelay{},
			demand: map[string]inputClasses{},
		}
		r.held[mac] = held
	}
	return held
}

// restore creates one controller's virtual devices from the snapshot
// its bond's Secret holds, before the controller has connected on this
// boot. Without it, a claim on a controller that is asleep would have
// no node to deliver until somebody pressed a button.
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
		fmt.Fprintf(os.Stderr, "relay: controller %s stored version %d capabilities, and this operator reads version %d\n",
			publishedMAC(mac), document.Version, snapshotVersion)
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	held := r.controller(mac)
	for _, caps := range document.Nodes {
		if _, found := held.nodes[caps.Name]; found {
			continue
		}
		relay := &nodeRelay{caps: caps}
		held.nodes[caps.Name] = relay
		r.create(mac, relay)
	}
}

// snapshot is what the bond's Secret stores under the evdev key: every
// node this controller registers, as JSON. It answers with nothing for
// a controller that has never connected, and the Secret then holds no
// snapshot either.
func (r *relays) snapshot(mac string) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	held := r.held[mac]
	if held == nil || len(held.nodes) == 0 {
		return nil
	}
	document := evdevSnapshot{Version: snapshotVersion}
	for _, relay := range held.nodes {
		document.Nodes = append(document.Nodes, relay.caps)
	}
	// The document is compared against the stored one byte for byte, so
	// the order has to come from the data and not from a map.
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

// virtualNodes are the nodes a claim on this controller delivers. The
// answer is empty only for a bond that has never connected since it
// was made, which is the state the no-input-node taint reports.
func (r *relays) virtualNodes(mac string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	held := r.held[mac]
	if held == nil {
		return nil
	}
	var nodes []string
	for _, relay := range held.nodes {
		if relay.device != nil {
			nodes = append(nodes, relay.device.node())
		}
	}
	slices.Sort(nodes)
	return nodes
}

// classes are the input classes one controller carries: the union of
// what udev would set on every node it registers. They come from the
// capabilities the relay holds, so a controller that connected once
// still reports them while it sleeps, from the snapshot in its bond's
// Secret. A bond that has never connected has no node here and
// carries no class.
func (r *relays) classes(mac string) inputClasses {
	r.mu.Lock()
	defer r.mu.Unlock()
	held := r.held[mac]
	if held == nil {
		return 0
	}
	var carried inputClasses
	for _, relay := range held.nodes {
		carried |= nodeInputClasses(relay.caps)
	}
	return carried
}

// stop takes one controller's virtual devices away. It runs during an
// unpair, after the claim that held the controller has been released.
// A teardown repeats a step whenever a pass fails, so stopping a
// controller that has no relay is success.
func (r *relays) stop(mac string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	held := r.held[mac]
	if held == nil {
		return
	}
	delete(r.held, mac)
	for _, relay := range held.nodes {
		if relay.source != nil {
			_ = relay.source.Close()
		}
		if relay.device != nil {
			_ = relay.device.close()
		}
	}
	fmt.Printf("relay: controller %s no longer answers on a virtual node\n", publishedMAC(mac))
}

// create builds one virtual device and holds it open. The caller holds
// the lock.
func (r *relays) create(mac string, relay *nodeRelay) bool {
	device, err := r.kernel.createVirtual(relay.caps, relayPhys(mac))
	if err != nil {
		fmt.Fprintf(os.Stderr, "relay: creating a virtual device for controller %s: %v\n", publishedMAC(mac), err)
		return false
	}
	relay.device = device
	if !withinDeliveredRange(device.node()) {
		// liken's adapter claim creates event0 through event31 in this
		// container, and a node above that range exists on the host alone. A
		// claim that delivered it would fail every container creation, because
		// the runtime creates the node from the major and minor of a node it
		// can read.
		fmt.Fprintf(os.Stderr, "relay: controller %s landed on %s, above the %d nodes this container holds; a claim on it cannot be prepared\n",
			publishedMAC(mac), device.node(), deliveredInputNodes)
	}
	fmt.Printf("relay: controller %s answers as %q on %s\n", publishedMAC(mac), relay.caps.Name, device.node())
	return true
}

// read opens a real node, limits it to what the controller's prepared
// claims demand, and starts moving its events. A fresh fd carries no
// mask, so the demand is applied before the pump reads anything. The
// caller holds the lock.
func (r *relays) read(mac string, relay *nodeRelay, path string, want inputClasses) {
	source, err := r.kernel.open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "relay: opening %s of controller %s: %v\n", path, publishedMAC(mac), err)
		return
	}
	relay.source, relay.sourcePath, relay.narrowed = source, path, false
	relay.narrow(mac, want)
	go r.pump(relay, source)
}

// pump moves events from one real node into its virtual device until
// the read fails, which is what a controller going to sleep looks
// like: the kernel removes the node and the read answers with an
// error. The relay then waits for the next ensure that names a node
// again.
//
// A pump whose node no prepared claim demands never wakes. The kernel
// queues nothing on the fd and drops the frame marker of an empty
// frame, so the goroutine stays blocked in the read, and the events
// cost this operator nothing.
//
// The events are bytes here, and nothing decodes them. A read returns
// whole input_event records, and the same bytes written to the uinput
// fd are the same events on the virtual device. A read error means the
// controller disconnected and its node is gone, so the pump closes the
// source and the relay waits for the next pass that names a node.
func (r *relays) pump(relay *nodeRelay, source realNode) {
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

// relayPhys names the virtual device's physical address: this driver
// and the controller whose events arrive on it. A person reading
// /proc/bus/input/devices reads which controller a virtual device
// stands for.
func relayPhys(mac string) string {
	return DriverName + "/" + normalizeMAC(mac)
}
