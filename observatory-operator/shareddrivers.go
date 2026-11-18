package main

// Two devices on one server can declare the same driver, such as a spare
// focuser next to the focuser in use. INDI names a device after its
// model, so both drivers would define a device with the same name. The
// second driver's definitions replace the first's in every client, and
// when the second driver exits it deletes the device that the first
// driver still runs. So a server runs each driver for one device only.
// The other device gets no pod and no driver, and its status is `Error`
// with the name of the device that runs the driver.
//
// The older device by creation time runs the driver. When both were
// created in the same second, the device whose kind and name sort first
// runs it. The order depends only on the two resources, so it does not
// change when a device starts or stops. `sharedDrivers` makes the
// choice and `tree.devicesOn` applies it, so pods, drivers, activation,
// and the status writer all agree.
//
// Activation fails on such a pair instead of skipping one device: the
// step that starts the server fails and names both devices and the
// driver. Otherwise the reservation would become Ready without one of
// its devices, such as the imaging camera, and only that device's
// status would show it. A device placed on a server during a session
// waits for the next session (`tree.server`), so it never replaces a
// device that the holder is using.

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// driverHolder returns the device that runs d's driver on d's server,
// and false when d runs it itself or shares no driver.
func (t *tree) driverHolder(d *device) (*device, bool) {
	t.driverHoldersOnce.Do(func() { t.driverHolders = t.sharedDrivers() })
	holder, taken := t.driverHolders[d]
	return holder, taken
}

// sharedDrivers maps each device that does not run its driver to the
// device on the same server that does. The status writer looks up every
// device on each pass, so the tree computes the map once.
func (t *tree) sharedDrivers() map[*device]*device {
	type serverDriver struct {
		server serverRef
		driver string
	}
	runs := map[serverDriver]*device{}
	devices := driverDevices(t.devices)
	for _, d := range devices {
		if ref, placed := t.server(d); placed {
			at := serverDriver{ref, d.object.Spec.Driver.Name}
			if other, ok := runs[at]; !ok || runsBefore(d, other) {
				runs[at] = d
			}
		}
	}
	taken := map[*device]*device{}
	for _, d := range devices {
		if ref, placed := t.server(d); placed {
			if holder := runs[serverDriver{ref, d.object.Spec.Driver.Name}]; holder != d {
				taken[d] = holder
			}
		}
	}
	return taken
}

// runsBefore reports whether a runs the driver instead of b when both
// declare the same driver on one server.
func runsBefore(a, b *device) bool {
	ca, cb := createdAt(a), createdAt(b)
	if !ca.Equal(cb) {
		return ca.Before(cb)
	}
	return a.key() < b.key()
}

func createdAt(d *device) time.Time {
	if at := d.object.Metadata.CreationTimestamp; at != nil {
		return *at
	}
	return time.Time{}
}

// driverConflicts returns an error that names each pair of devices on
// the server that declare the same driver, or nil when there is none.
func (t *tree) driverConflicts(ref serverRef) error {
	var pairs []string
	for _, d := range t.placedOn(ref) {
		holder, taken := t.driverHolder(d)
		if !taken {
			continue
		}
		pair := []*device{holder, d}
		slices.SortFunc(pair, func(a, b *device) int { return strings.Compare(a.key(), b.key()) })
		pairs = append(pairs, fmt.Sprintf("%s and %s share driver %s on server %s", names(pair[:1]), names(pair[1:]), d.object.Spec.Driver.Name, ref))
	}
	if len(pairs) == 0 {
		return nil
	}
	return fmt.Errorf("%s: INDI gives both devices the same name. Move one device of each pair to another server, or remove its parent from its spec", strings.Join(pairs, "; "))
}

// sharedDriver returns the status message of a device whose driver
// holder runs on the same server.
func sharedDriver(holder *device, ref serverRef) string {
	return fmt.Sprintf("%s %s runs driver %s on server %s already: INDI gives both devices the same name. Move one of them to another server, or remove its parent from its spec",
		holder.kind.Name, holder.name(), holder.object.Spec.Driver.Name, ref)
}
