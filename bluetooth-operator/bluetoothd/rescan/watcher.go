package main

// The watcher reads each adapter's monitor packets, and sends Add
// Device again for a device when the kernel leaves its scan off after a
// failed connection.

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"time"

	"github.com/liken-sh/liken/bluetooth-operator/bluetoothd/mgmt"
)

// watcher keeps one radio for each adapter that appears on the monitor
// channel.
type watcher struct {
	radios map[uint16]*radio

	// management opens a Management API socket. Each repair opens its
	// own and closes it afterwards, because an open socket receives the
	// events of every adapter, and when its queue fills with unread
	// events the kernel drops the reply that a repair waits for.
	management func() (io.ReadWriteCloser, error)

	log io.Writer
}

func newWatcher(management func() (io.ReadWriteCloser, error), log io.Writer) *watcher {
	return &watcher{radios: map[uint16]*radio{}, management: management, log: log}
}

// run processes monitor packets and repair deadlines until ctx ends,
// when it returns nil, or until the monitor channel fails, when it
// returns that error. The timer runs only while a repair or check is
// pending.
func (w *watcher) run(ctx context.Context, packets <-chan packet, failed <-chan error) error {
	timer := time.NewTimer(0)
	<-timer.C
	defer timer.Stop()
	for {
		var due <-chan time.Time
		if deadline, ok := w.next(); ok {
			timer.Reset(time.Until(deadline))
			due = timer.C
		}
		select {
		case <-ctx.Done():
			return nil
		case err := <-failed:
			return err
		case p := <-packets:
			w.observe(p, time.Now())
		case <-due:
			w.act(time.Now())
		}
		timer.Stop()
	}
}

func (w *watcher) observe(p packet, now time.Time) {
	if p.kind == monitorDelIndex {
		// The kernel removed the adapter and its devices.
		delete(w.radios, p.index)
		return
	}
	r, known := w.radios[p.index]
	if !known {
		r = newRadio()
		w.radios[p.index] = r
	}
	r.observe(p, now)
}

func (w *watcher) next() (time.Time, bool) {
	var earliest time.Time
	for _, r := range w.radios {
		if due, ok := r.next(); ok && (earliest.IsZero() || due.Before(earliest)) {
			earliest = due
		}
	}
	return earliest, !earliest.IsZero()
}

// act sends Add Device for each repair whose deadline has passed. It
// logs each device whose scan has still not started rescanSettle after
// its repair.
func (w *watcher) act(now time.Time) {
	for _, index := range slices.Sorted(maps.Keys(w.radios)) {
		r := w.radios[index]
		for d, check := range r.checks {
			if now.Before(check) {
				continue
			}
			delete(r.checks, d)
			fmt.Fprintf(w.log, "start-rescan: hci%d: the kernel did not scan within %s after %s was added again, so the device cannot reconnect until the adapter powers off and on\n",
				index, rescanSettle, d)
		}
		for d, waiting := range r.repairs {
			if now.Before(waiting.due) {
				continue
			}
			delete(r.repairs, d)
			if err := w.addDevice(index, d); err != nil {
				fmt.Fprintf(w.log, "start-rescan: hci%d: %s and the kernel did not scan again within %s, and adding the device again returned: %v\n",
					index, waiting.cause, rescanSettle, err)
				continue
			}
			r.checks[d] = now.Add(rescanSettle)
			fmt.Fprintf(w.log, "start-rescan: hci%d: %s and the kernel did not scan again within %s, so start-rescan added the device again to make the kernel scan for it\n",
				index, waiting.cause, rescanSettle)
		}
	}
}

// addDevice sends Add Device for d twice: first with the action that
// allows an incoming connection, then with auto connect, the action
// that bluetoothd set. One Add Device with auto connect would change
// nothing, because hci_conn_params_set in net/bluetooth/mgmt.c returns
// early when the action is the same. Each change of action takes the
// unconnected device off the kernel's list and adds it back, and each
// command ends in hci_update_passive_scan, which starts the scan.
func (w *watcher) addDevice(index uint16, d device) error {
	channel, err := w.management()
	if err != nil {
		return err
	}
	defer channel.Close()
	if err := mgmt.AddDevice(channel, index, d.address, d.kind, mgmt.ActionAllowIncoming); err != nil {
		return err
	}
	return mgmt.AddDevice(channel, index, d.address, d.kind, mgmt.ActionAutoConnect)
}
