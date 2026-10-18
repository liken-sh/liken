package main

// The loop that reads packets, waits for deadlines, and sends each
// repair.

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"time"

	"github.com/liken-sh/bluetooth-operator/bluetoothd/mgmt"
)

// watcher holds one radio for each adapter the monitor channel names.
type watcher struct {
	radios map[uint16]*radio

	// management opens the Management API for one repair. Each repair
	// opens its own socket and closes it, because a socket that stays
	// open receives every event of every adapter, and an unread queue
	// that fills drops the reply a repair waits for.
	management func() (io.ReadWriteCloser, error)

	log io.Writer
}

func newWatcher(management func() (io.ReadWriteCloser, error), log io.Writer) *watcher {
	return &watcher{radios: map[uint16]*radio{}, management: management, log: log}
}

// run handles each packet and each deadline until ctx ends or the
// monitor channel fails. Only the deadlines of repairs and checks set
// the timer, so the loop sleeps while no attempt has failed.
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
		// The adapter is gone, and the kernel freed its devices.
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

// act sends each repair and writes each check whose deadline has
// passed.
func (w *watcher) act(now time.Time) {
	for _, index := range slices.Sorted(maps.Keys(w.radios)) {
		r := w.radios[index]
		for d, check := range r.checks {
			if now.Before(check) {
				continue
			}
			delete(r.checks, d)
			fmt.Fprintf(w.log, "start-rescan: hci%d: the kernel did not scan within %s after %s was added again; the device cannot reconnect until the adapter powers off and on\n",
				index, rescanSettle, d)
		}
		for d, waiting := range r.repairs {
			if now.Before(waiting.due) {
				continue
			}
			delete(r.repairs, d)
			if err := w.addDevice(index, d); err != nil {
				fmt.Fprintf(w.log, "start-rescan: hci%d: a connection to %s failed with HCI status 0x%02x and the kernel did not scan again within %s; adding the device again failed: %v\n",
					index, d, waiting.status, rescanSettle, err)
				continue
			}
			r.checks[d] = now.Add(rescanSettle)
			fmt.Fprintf(w.log, "start-rescan: hci%d: a connection to %s failed with HCI status 0x%02x and the kernel did not scan again within %s; added the device again, so the kernel scans for it\n",
				index, d, waiting.status, rescanSettle)
		}
	}
}

// addDevice sends the Add Device command that bluetoothd sent for d.
func (w *watcher) addDevice(index uint16, d device) error {
	channel, err := w.management()
	if err != nil {
		return err
	}
	defer channel.Close()
	return mgmt.AddDevice(channel, index, d.address, d.kind, mgmt.ActionAutoConnect)
}
