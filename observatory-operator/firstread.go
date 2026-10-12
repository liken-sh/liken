package main

// A new copy of the operator starts with the statuses that the last
// copy wrote, and learns each device's state again only when its INDI
// client reads the device from its server. A status composed before
// that read says that the device is Starting and leaves out its state
// conditions, such as a weather station's Safe. The read then sets them
// again with a new transition time. A trigger runs once for each
// transition of its condition (triggers.go), so without a hold a park
// on Safe=False runs again after each restart of the operator in bad
// weather.
//
// So a new copy writes no status until it has read each device whose
// stored phase is Connected. The hold covers every status, and not
// only the devices' own, because the status of a Telescope, an
// Observatory, and an OpticalTrain is composed from the statuses of
// their devices. The run records and the activity come from the stored
// statuses (operator.go), so the triggers and the runners act on the
// stored statuses during the hold, as the last copy did.

import (
	"context"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// readLimit bounds the hold. A server that does not answer within it
// leaves the stored statuses of its devices out of date, and the writer
// then writes what the operator reads.
const readLimit = 30 * time.Second

// firstRead is the status writer's record of the devices that this copy
// of the operator has read.
type firstRead struct {
	deadline time.Time
	done     bool
	// read holds each device that this copy has read, as
	// "<server>/<INDI device>".
	read map[string]bool
}

func newFirstRead() *firstRead {
	return &firstRead{deadline: time.Now().Add(readLimit), read: map[string]bool{}}
}

// hold reads each device whose stored phase is Connected, and answers
// the end of the hold while a device is not read, or zero once the
// writer may write.
func (f *firstRead) hold(ctx context.Context, o *operator, t *tree) time.Time {
	if f.done || !time.Now().Before(f.deadline) {
		f.done = true
		return time.Time{}
	}
	held := false
	for _, d := range t.devices {
		if d.object.Status.Phase == observatory.DeviceConnected && !f.readDevice(ctx, o, t, d) {
			held = true
		}
	}
	if !held {
		f.done = true
		return time.Time{}
	}
	return f.deadline
}

// readDevice reports whether this copy has read one device from its
// server. INDI marks no end to the definitions that answer a client's
// first getProperties, so the client syncs the device (indi.Client.Sync):
// the driver defines CONNECTION again after every property it defined
// before. A sync makes the driver define every property again for each
// client of the server, so the writer syncs each device once, when the
// copy starts. A device whose driver or server has no Ready pod has
// nothing to read, and its stored status is out of date.
func (f *firstRead) readDevice(ctx context.Context, o *operator, t *tree, d *device) bool {
	ref, placed := t.server(d)
	if _, _, hasPod := t.podOf(d); !hasPod || !placed {
		return true
	}
	if p, ok := t.pods[ref.String()]; !ok || !p.ready() {
		return true
	}
	server, open := o.servers.get(ref.String())
	if !open || !server.client.Connected() {
		return false
	}
	name := t.indiName(server.client, d)
	if name == "" {
		return false
	}
	key := ref.String() + "/" + name
	if f.read[key] {
		return true
	}
	if _, defined := server.client.Property(name, "CONNECTION"); !defined {
		return false
	}
	wait, cancel := context.WithDeadline(ctx, f.deadline)
	defer cancel()
	if err := server.client.Sync(wait, name); err != nil {
		return false
	}
	f.read[key] = true
	return true
}
