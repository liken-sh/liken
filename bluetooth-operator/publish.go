package main

// The publisher writes this node's ResourceSlice. On each pass it reads
// the controllers that bluetoothd has paired, finds each controller's
// evdev nodes in sysfs, points each controller's relay at those nodes,
// and writes the slice.
//
// The slice lists every paired controller, and the media bus device
// once bluetoothd has reported the adapter. A controller that is
// switched off, or whose radio is unplugged, stays in the slice with
// a taint, so that the claims allocated to it still name a published
// device. A controller leaves the slice only while its Peripheral is
// being deleted, through excluded.

import (
	"errors"
	"fmt"
	"os"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// pairedSetReader returns the controllers that bluetoothd has bonded,
// keyed by MAC. A test passes one that returns a fixed set, or
// ErrNoAdapter, without a D-Bus connection.
type pairedSetReader func() (map[string]controller, error)

// publisher writes one node's ResourceSlice. It keeps the last paired
// set that bluetoothd returned, because bluetoothd removes its device
// objects when the adapter is unplugged, and the publisher still has to
// publish those controllers with a taint.
type publisher struct {
	client   *apiclient.Client
	nodeName string
	owner    OwnerReference
	known    map[string]controller

	// excluded is the set of controllers being unpaired, as passed to the
	// last reconcile. attach skips them, as reconcile does.
	excluded map[string]bool

	// relays has one virtual input device for each evdev node of each
	// bonded controller. A pod with a claim gets the virtual node, so
	// the no-input-node taint depends on the relay's nodes and not on
	// the nodes in sysfs.
	relays *relays

	// reported is the set of controllers whose nodes had moved at the
	// last pass, so that reportMoved prints each one once.
	reported map[string]bool

	// adapter is the address of the radio this pod uses. Discovery
	// keeps only the controllers on this radio, and the media bus
	// device is named for it. reconcile sets it from the first adapter
	// that bluetoothd reports and never changes it, because a pod uses
	// one radio.
	adapter bonds.Address
}

// reconcile updates every controller's relay and writes the
// ResourceSlice from what bluetoothd and sysfs report now. It returns
// false when the caller should retry soon: when the pass failed, when
// there was nothing to publish yet, and after it wrote a slice for an
// adapter that is gone.
//
// excluded has the controllers of Peripherals that are being deleted.
// reconcile leaves them out of the slice so that no new claim is
// allocated to a controller that is about to be unpaired. Their bonds
// are still in bluetoothd during this call, so the caller has to
// supply the set.
//
// The relays are updated before the slice is built, so a controller is
// published as usable in the same pass that creates its virtual node.
func (p *publisher) reconcile(readPairedSet pairedSetReader, readAdapter adapterAddressReader, excluded map[string]bool) bool {
	p.excluded = excluded

	// Until bluetoothd reports an adapter, the address is zero and
	// discovery keeps every device.
	if p.adapter.IsZero() {
		if address, err := readAdapter(); err == nil {
			p.adapter = address
		}
	}
	nodes := nodesByMAC(discoverHIDDevices(draSysfsRoot, p.adapter))

	// busReachable is false when the adapter is gone, although the
	// D-Bus socket to bluetoothd still works.
	busReachable := true

	controllers, err := readPairedSet()
	switch {
	case errors.Is(err, ErrNoAdapter) && len(p.known) == 0 && p.adapter.IsZero():
		// bluetoothd has not exported an adapter yet, so there is
		// nothing to publish. After an adapter has been seen,
		// ErrNoAdapter means it is gone and takes the next case, even
		// with nothing paired, so that the media bus gets its taint.
		fmt.Fprintf(os.Stderr, "waiting for bluetoothd to publish an adapter\n")
		return false

	case errors.Is(err, ErrNoAdapter):
		// The adapter was unplugged or reset over USB. Every controller
		// on it is published as disconnected, and the taint evicts the
		// pods that use them and keeps new claims off them until the
		// radio returns. The relays keep their virtual nodes, so a pod
		// that tolerates the taint has a working node again when the
		// radio comes back.
		fmt.Fprintf(os.Stderr, "the Bluetooth adapter is gone, so the operator taints the media bus and all %d published controllers\n", len(p.known))
		controllers, nodes, busReachable = unreachable(p.known), nil, false

	case err != nil:
		fmt.Fprintf(os.Stderr, "reading the paired controllers: %v\n", err)
		return false

	default:
		p.known = controllers
	}

	published := without(controllers, excluded)
	virtual, classes := p.relay(published, nodes)
	moved := movedControllers(virtual)
	p.reportMoved(moved)
	devices := sliceDevices(published, virtual, classes, moved)
	// The media bus device goes first, so that the truncation below
	// drops controllers and never the machine's Bluetooth audio.
	if !p.adapter.IsZero() {
		devices = append([]SliceDevice{mediaBusDevice(p.adapter.String(), busReachable)}, devices...)
	}
	if len(devices) > maxSliceDevices {
		fmt.Fprintf(os.Stderr, "%d devices exceed the capacity of one ResourceSlice, %d, so the operator publishes only the first %d\n",
			len(devices), maxSliceDevices, maxSliceDevices)
		devices = devices[:maxSliceDevices]
	}
	if err := EnsureResourceSlice(p.client, p.nodeName, p.owner, devices); err != nil {
		fmt.Fprintf(os.Stderr, "writing the ResourceSlice: %v\n", err)
		return false
	}
	// err is still the error of readPairedSet. When the adapter is
	// gone, return false so that the caller retries soon and finds an
	// adapter that comes back from a USB reset.
	return !errors.Is(err, ErrNoAdapter)
}

// relay points each controller's relay at its current evdev nodes. It
// returns, by MAC, the virtual nodes that a claim on the controller
// gets and the input classes the controller has.
//
// Controllers being unpaired are not in the map, so their relays are
// not started again after unpair.go stops them.
func (p *publisher) relay(controllers map[string]controller, nodes map[string][]string) (map[string][]string, map[string]inputClasses) {
	virtual := make(map[string][]string, len(controllers))
	classes := make(map[string]inputClasses, len(controllers))
	for mac := range controllers {
		p.relays.ensure(mac, nodes[mac])
		virtual[mac] = p.relays.virtualNodes(mac)
		classes[mac] = p.relays.classes(mac)
	}
	return virtual, classes
}

// attach starts relays for the evdev nodes that published controllers
// have registered since the last pass. The loop calls it on each
// uevent without waiting for settleWindow, because the kernel does not
// queue input events for an evdev node that nobody has open, and a
// button press before the relay opens the node is lost. The pass at
// the end of the window then writes the slice.
//
// A controller paired since the last pass gets its relay from the pass
// that first publishes it.
func (p *publisher) attach() {
	nodes := nodesByMAC(discoverHIDDevices(draSysfsRoot, p.adapter))
	for mac := range without(p.known, p.excluded) {
		if len(nodes[mac]) > 0 {
			p.relays.ensure(mac, nodes[mac])
		}
	}
}

// reportMoved prints a line for each controller that was not in the
// moved set at the last pass.
func (p *publisher) reportMoved(moved map[string]bool) {
	for mac := range moved {
		if !p.reported[mac] {
			fmt.Printf("controller %s: the device nodes in a prepared container differ from the current ones; evicting the pod that uses it\n", publishedMAC(mac))
		}
	}
	p.reported = moved
}

// without returns the controllers that are not in excluded. When
// excluded is empty, it returns the same map and not a copy.
func without(controllers map[string]controller, excluded map[string]bool) map[string]controller {
	if len(excluded) == 0 {
		return controllers
	}
	kept := make(map[string]controller, len(controllers))
	for mac, c := range controllers {
		if excluded[mac] {
			continue
		}
		kept[mac] = c
	}
	return kept
}

// unreachable returns a copy of the last known controllers with
// Connected set to false, so that the connected attribute agrees with
// the disconnected taint.
func unreachable(known map[string]controller) map[string]controller {
	out := make(map[string]controller, len(known))
	for mac, c := range known {
		c.Connected = false
		out[mac] = c
	}
	return out
}
