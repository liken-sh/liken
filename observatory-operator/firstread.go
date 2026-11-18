package main

// After a restart, the operator learns a device's state only when its
// INDI client has read the device from the server. A status written
// before that read shows the device as Starting without its state
// conditions, such as a weather station's Safe, and the read then sets
// them again with a new transition time. A trigger runs once for each
// transition of its condition (triggers.go), so a park on Safe=False
// would run again after every restart in bad weather.
//
// So the status writer writes no status until it has read each device
// whose stored phase is Connected. It holds back every status, not only
// the devices', because the statuses of a Telescope, an Observatory,
// and an OpticalTrain are composed from their devices' statuses.
// Triggers and runners keep working during the hold, because they read
// run records and activity from the stored statuses (operator.go).

import (
	"context"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// readLimit bounds the hold, so a driver that never answers does not
// stop every status write. After readLimit the writer writes the
// statuses from what the operator has read so far.
const readLimit = 30 * time.Second

type firstRead struct {
	deadline time.Time
	done     bool
	// read is keyed by "<server>/<INDI device>".
	read map[string]bool
}

func newFirstRead() *firstRead {
	return &firstRead{deadline: time.Now().Add(readLimit), read: map[string]bool{}}
}

// hold reads each device whose stored phase is Connected. While a
// device is unread it returns the deadline, and once the writer may
// write it returns the zero time.
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

// readDevice reports whether the device has been read from its server.
// INDI does not mark the end of the definitions that answer a client's
// first getProperties, so readDevice waits on indi.Client.Sync. A sync
// makes the driver define every property again for every client of the
// server, so readDevice syncs each device once. A device whose driver
// pod or server pod is missing or not Ready has nothing to read, so the
// hold does not wait for it.
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
