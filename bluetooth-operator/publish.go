package main

// Publishing this node's paired controllers, one pass at a time.
//
// The publisher is the half of the loop that reports what this node
// can deliver right now. It re-reads bluetoothd's paired set,
// re-walks sysfs for the evdev nodes each controller registers, moves
// each controller's relay onto the nodes it registers now, and writes
// the ResourceSlice when any of that moved.
//
// Nothing here treats a device as gone. Membership in the slice is
// the paired set, plus the media bus once bluetoothd has named the
// adapter, and a controller that is switched off or a radio that
// is unplugged is published with taints instead, because a device that
// leaves the slice while a claim names it strands the consumer. The
// one removal that does not follow from the paired set comes from the
// caller's keepOut, and that removal is the step of an unpairing that
// runs before the bond is removed.

import (
	"errors"
	"fmt"
	"os"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// pairedSetReader returns the controllers bluetoothd holds.
// reconcile takes one as a parameter rather than calling bluetoothd
// itself, so that a test can supply a result, including ErrNoAdapter,
// without a bus.
type pairedSetReader func() (map[string]controller, error)

// publisher writes one node's slice. It holds the last paired set
// that bluetoothd returned, because an adapter that departs takes its
// device objects out of the tree, and this record is then the only
// account of which controllers the slice offers.
type publisher struct {
	client   *Client
	nodeName string
	owner    OwnerReference
	known    map[string]controller

	// relays holds one virtual input device for each evdev node each
	// bonded controller registers. The claim delivers the virtual node,
	// so the publisher derives the no-input-node taint from the relay
	// and not from the sysfs walk.
	relays *relays

	// reported names the controllers the last pass found with moved nodes,
	// so the line about a stuck consumer prints once and not on every
	// pass until the eviction lands.
	reported map[string]bool

	// adapter is the radio this pod serves. It scopes discovery to the
	// controllers on this operator's own adapter, and it names the
	// media bus device the slice publishes. It is read from
	// bluetoothd the first time bluetoothd replies, and then it is fixed
	// for the life of the process, because a pod serves one adapter and
	// re-reading it could point the filter at a different radio.
	adapter bonds.Address
}

// reconcile makes the published slice and every controller's relay
// agree with what bluetoothd and sysfs report right now. It reports
// whether the pass left the node's state correct, so that the caller
// can run a failure again shortly.
//
// keepOut names the controllers a teardown has retired. A Peripheral
// that is being deleted takes its device out of the published inventory
// before the bond is removed, so that no new claim can be allocated to
// a controller whose bond is about to be removed. The bond is still in
// bluetoothd when this runs, which is why the exclusion comes from the
// caller and not from the paired set.
//
// The order matters. Each controller's relay moves onto the nodes the
// controller registers now before the slice is built, so the slice
// reports a controller as usable in the same pass that gives it a
// virtual node.
func (p *publisher) reconcile(readPairedSet pairedSetReader, readAdapter adapterAddressReader, keepOut map[string]bool) bool {
	// The adapter address scopes discovery to this operator's own radio.
	// Until bluetoothd replies, the address stays zero and discovery
	// keeps every device, which is correct on the single-adapter machine
	// this runs on and no worse than the pre-filter walk on the startup
	// window of any machine.
	if p.adapter.IsZero() {
		if address, err := readAdapter(); err == nil {
			p.adapter = address
		}
	}
	nodes := nodesByMAC(discoverHIDDevices(draSysfsRoot, p.adapter))

	// busReachable is false only when the adapter has departed: the bus
	// socket still exists, but the radio behind it is gone.
	busReachable := true

	controllers, err := readPairedSet()
	switch {
	case errors.Is(err, ErrNoAdapter) && len(p.known) == 0 && p.adapter.IsZero():
		// The startup window. bluetoothd has not published its object
		// tree yet, so there is no last-known set to taint and nothing
		// true to publish about this node. Once bluetoothd has named
		// the adapter, an ErrNoAdapter is a departure and takes the
		// branch below even with nothing paired, so the media bus
		// picks up its taint.
		fmt.Fprintf(os.Stderr, "waiting for bluetoothd to publish an adapter\n")
		return false

	case errors.Is(err, ErrNoAdapter):
		// The adapter departed, by an unplug or a USB reset. Every
		// controller it held is out of reach, and the slice must report
		// that rather than report nothing: the devices stay, so no
		// allocation is stranded, and the disconnected taint goes on, so
		// the eviction controller ends the sessions that are already
		// running and no new claim is allocated while the radio is
		// away. The relays keep their virtual nodes, so a consumer that
		// tolerates the taint holds a node that works again when the
		// radio returns.
		fmt.Fprintf(os.Stderr, "the adapter is gone; tainting the media bus and all %d published controllers\n", len(p.known))
		controllers, nodes, busReachable = unreachable(p.known), nil, false

	case err != nil:
		fmt.Fprintf(os.Stderr, "reading the paired set: %v\n", err)
		return false

	default:
		p.known = controllers
	}

	published := without(controllers, keepOut)
	virtual := p.relay(published, nodes)
	moved := movedControllers(virtual)
	p.reportMoved(moved)
	devices := sliceDevices(published, virtual, moved)
	// The media bus joins every slice this pass publishes, as soon as
	// bluetoothd has named the adapter. It goes at the front of the
	// list so the overflow truncation below can only drop controllers:
	// the bus is one device for the whole radio, and dropping it would
	// remove the machine's Bluetooth audio to make room for one more
	// controller.
	//
	// With the bus in the list, a node that has an adapter always has
	// a slice, and the delete path runs only while the adapter is
	// still unknown.
	if !p.adapter.IsZero() {
		devices = append([]SliceDevice{mediaBusDevice(p.adapter.String(), busReachable)}, devices...)
	}
	if len(devices) > maxSliceDevices {
		fmt.Fprintf(os.Stderr, "%d devices exceed one slice's capacity of %d; dropping the overflow\n",
			len(devices), maxSliceDevices)
		devices = devices[:maxSliceDevices]
	}
	if err := EnsureResourceSlice(p.client, p.nodeName, p.owner, devices); err != nil {
		fmt.Fprintf(os.Stderr, "publishing the slice: %v\n", err)
		return false
	}
	// A tainted slice is correct but degraded. Reporting it as
	// unfinished triggers one quick retry, which catches an adapter
	// that comes back from a USB reset a second later.
	return !errors.Is(err, ErrNoAdapter)
}

// relay makes each published controller's virtual input devices agree
// with the evdev nodes the controller registers now, and answers with
// the nodes a claim on each controller delivers.
//
// A controller a teardown has retired is not in this set, so its relay
// is neither started again nor asked for a node. unpair.go stops that
// relay in the same step that takes the device out of the slice.
func (p *publisher) relay(controllers map[string]controller, nodes map[string][]string) map[string][]string {
	virtual := make(map[string][]string, len(controllers))
	for mac := range controllers {
		p.relays.ensure(mac, nodes[mac])
		virtual[mac] = p.relays.virtualNodes(mac)
	}
	return virtual
}

// reportMoved prints one line for each controller that joins the moved
// set, and remembers the set for the next pass.
func (p *publisher) reportMoved(moved map[string]bool) {
	for mac := range moved {
		if !p.reported[mac] {
			fmt.Printf("controller %s was delivered nodes that moved; evicting its consumer\n", publishedMAC(mac))
		}
	}
	p.reported = moved
}

// without copies the paired set with the retired controllers left out.
// It returns the same map when nothing is retired, which is every
// pass on a machine where nobody is unpairing anything.
func without(controllers map[string]controller, keepOut map[string]bool) map[string]controller {
	if len(keepOut) == 0 {
		return controllers
	}
	kept := make(map[string]controller, len(controllers))
	for mac, c := range controllers {
		if keepOut[mac] {
			continue
		}
		kept[mac] = c
	}
	return kept
}

// unreachable copies the last-known controllers with every one of
// them marked disconnected. The connected attribute must agree with
// the taints: an adapter that is gone holds no connection, whatever
// bluetoothd last reported.
func unreachable(known map[string]controller) map[string]controller {
	out := make(map[string]controller, len(known))
	for mac, c := range known {
		c.Connected = false
		out[mac] = c
	}
	return out
}
