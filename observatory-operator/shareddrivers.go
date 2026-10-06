package main

// Two devices on one server can name the same driver, such as a spare
// focuser installed beside a focuser that runs. INDI names a device
// after its model, so both drivers define one device name. The second
// driver's definitions replace the first's in every client, and its
// exit deletes the device that the first driver still runs. So a
// server runs one device for each driver, and the other never starts:
// it gets no pod and no driver, and reports `Error` with the name of
// the device that runs the driver.
//
// The device that runs is, in order:
//
//  1. the device that the server runs now, from its pod's annotation,
//     so a device that joins never stops one that a holder uses;
//  2. the device with the older resource, by creation time, so the
//     choice does not change while neither runs;
//  3. the device whose kind and name sort first, between devices
//     created in the same second.
//
// The tree makes the choice (devicesOn), so the runner's pods and
// drivers, activation, and the status writer all use the same one.

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// driverTakenBy answers the device that runs a device's driver on the
// device's server in its place.
func (t *tree) driverTakenBy(d *device) (*device, bool) {
	t.takenOnce.Do(func() { t.taken = t.sharedDrivers() })
	holder, taken := t.taken[d]
	return holder, taken
}

// sharedDrivers answers, for each device whose driver another device
// on its server runs, that other device. The status writer asks for
// every device on each pass, so a tree computes the answer once.
func (t *tree) sharedDrivers() map[*device]*device {
	type slot struct {
		server serverRef
		driver string
	}
	runs := map[slot]*device{}
	for _, d := range t.devices {
		if ref, placed := t.server(d); placed {
			at := slot{ref, d.object.Spec.Driver.Name}
			if other, ok := runs[at]; !ok || t.runsBefore(ref, d, other) {
				runs[at] = d
			}
		}
	}
	taken := map[*device]*device{}
	for _, d := range t.devices {
		if ref, placed := t.server(d); placed {
			if holder := runs[slot{ref, d.object.Spec.Driver.Name}]; holder != d {
				taken[d] = holder
			}
		}
	}
	return taken
}

// runsBefore reports whether a server runs device a rather than device
// b, when both name one driver.
func (t *tree) runsBefore(ref serverRef, a, b *device) bool {
	if ra, rb := t.listed(ref, a), t.listed(ref, b); ra != rb {
		return ra
	}
	ca, cb := createdAt(a), createdAt(b)
	if !ca.Equal(cb) {
		return ca.Before(cb)
	}
	return a.key() < b.key()
}

// listed reports whether a server's pod names a device in its
// annotation, which is the list of devices whose drivers it runs.
func (t *tree) listed(ref serverRef, d *device) bool {
	server, ok := t.pods[ref.String()]
	if !ok {
		return false
	}
	object, err := objectName(d.kind, d.name())
	if err != nil {
		return false
	}
	return slices.Contains(strings.Fields(server.Metadata.Annotations[annotationDrivers]), object+":"+strconv.Itoa(devicePort))
}

// createdAt answers a device's creation time, or the zero time when the
// resource has none.
func createdAt(d *device) time.Time {
	if at := d.object.Metadata.CreationTimestamp; at != nil {
		return *at
	}
	return time.Time{}
}

// sharedDriver is the fault of a device whose driver another device on
// the server runs.
func sharedDriver(holder *device, ref serverRef) string {
	return fmt.Sprintf("%s %s runs driver %s on server %s already: INDI gives both devices one name. Move one of them to another server, or to the shelf",
		holder.kind.Name, holder.name(), holder.object.Spec.Driver.Name, ref)
}
