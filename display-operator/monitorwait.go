package main

// The compositor role's wait for a monitor.
//
// Weston exits with code 1 at once when no connector on its card has a
// monitor. The kubelet restarts the container, and its backoff grows
// to five minutes, so a machine with no monitor shows a crash loop and
// a monitor that arrives waits up to five minutes for the next try.
// So the role waits here, with the container Running, until a monitor
// is on some connector, and only then execs weston.
//
// The wait reads the kernel's uevent socket and not a clock. A
// monitor that arrives raises a drm hotplug event within a moment, so
// the event is the earliest signal there is, and a poll would only
// add delay and wake a sleeping machine for nothing.

import (
	"context"
	"fmt"
)

// waitForMonitor blocks until one of the card's connectors has a
// monitor, and returns that output.
//
// The caller opens the listener before this function makes its first
// sysfs read, so a monitor that arrives between the read and the
// listen is not missed: its event is already in the channel. An event
// says only that something changed and never what is there, so every
// event re-reads sysfs. The roots are parameters so a test drives the
// wait over a tree it built and a channel it owns.
func waitForMonitor(ctx context.Context, sysRoot, card string, events <-chan drmEvent) (Output, error) {
	outputs := discoverOutputs(sysRoot, card)
	if live := connected(outputs); len(live) > 0 {
		return live[0], nil
	}
	fmt.Printf("%s: %s has no monitor on any of its %d connectors; waiting for one\n",
		DriverName, card, len(outputs))
	for {
		if err := awaitDRMEvent(ctx, events); err != nil {
			return Output{}, err
		}
		live := connected(discoverOutputs(sysRoot, card))
		if len(live) == 0 {
			continue
		}
		monitor := monitorID(live[0].Monitor)
		if monitor == "" {
			monitor = "a monitor with no readable EDID"
		}
		fmt.Printf("%s: %s has %s\n", DriverName, live[0].Connector, monitor)
		return live[0], nil
	}
}

// awaitDRMEvent blocks for one event, and reports the two ways the
// wait ends with no monitor: the context ends, or the listener's
// channel closes because its reader loop stopped. A closed channel
// delivers zero values forever, so a caller that read it as an event
// would spin.
func awaitDRMEvent(ctx context.Context, events <-chan drmEvent) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case _, open := <-events:
		if !open {
			return fmt.Errorf("the listener for kernel events stopped before a monitor arrived")
		}
		return nil
	}
}
