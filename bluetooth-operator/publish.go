package main

// Publishing this node's paired controllers, one pass at a time.
//
// The publisher is the half of the loop that reports what this node
// can deliver right now. It re-reads bluetoothd's paired set,
// re-walks sysfs for the evdev nodes each controller registers, keeps
// every prepared claim's CDI spec current, and writes the
// ResourceSlice when any of that moved.
//
// Nothing here treats a controller as gone. Membership in the slice is
// the paired set, and a controller that is switched off or a radio that
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

	// adapter is the radio this pod serves. It scopes discovery to the
	// controllers on this operator's own adapter. It is read from
	// bluetoothd the first time bluetoothd replies, and then it is fixed
	// for the life of the process, because a pod serves one adapter and
	// re-reading it could point the filter at a different radio.
	adapter bonds.Address
}

// reconcile makes the published slice and every prepared CDI spec
// agree with what bluetoothd and sysfs report right now. It reports
// whether the pass left the node's state correct, so that the caller
// can run a failure again shortly.
//
// keepOut names the controllers a teardown has retired. A Pairing that
// is being deleted takes its device out of the published inventory
// before the bond is removed, so that no new claim can be allocated to
// a controller whose bond is about to be removed. The bond is still
// in bluetoothd when this runs, which is why the exclusion comes from
// the caller and not from the paired set.
//
// The order matters. The CDI refresh runs first, so that a controller
// which came back on a different evdev node has a correct spec before
// the slice reports the controller as usable again.
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
	refreshCDISpecs(nodes)

	controllers, err := readPairedSet()
	switch {
	case errors.Is(err, ErrNoAdapter) && len(p.known) == 0:
		// The startup window. bluetoothd has not published its object
		// tree yet, so there is no last-known set to taint and nothing
		// true to publish about this node.
		fmt.Fprintf(os.Stderr, "waiting for bluetoothd to publish an adapter\n")
		return false

	case errors.Is(err, ErrNoAdapter):
		// The adapter departed, by an unplug or a USB reset. Every
		// controller it held is out of reach, and the slice must report
		// that rather than report nothing: the devices stay, so no
		// allocation is stranded, and both taints go on, so the
		// eviction controller ends the sessions that are already
		// running and the next claim parks instead of failing in
		// prepare. Passing no nodes is what derives both taints, which
		// is the same rule a single controller going quiet takes.
		fmt.Fprintf(os.Stderr, "the adapter is gone; taints all %d published controllers\n", len(p.known))
		controllers, nodes = unreachable(p.known), nil

	case err != nil:
		fmt.Fprintf(os.Stderr, "reading the paired set: %v\n", err)
		return false

	default:
		p.known = controllers
	}

	devices := sliceDevices(without(controllers, keepOut), nodes)
	if len(devices) > maxSliceDevices {
		fmt.Fprintf(os.Stderr, "%d paired controllers exceed one slice's capacity of %d; dropping the overflow\n",
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
