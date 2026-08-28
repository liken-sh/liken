package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// darkSysfs is the machine the wait exists for: a card whose three
// connectors are all registered and none has a monitor on it.
func darkSysfs(t *testing.T) string {
	t.Helper()
	return fakeSysfs(t, "card1", map[string]string{
		"HDMI-A-1": "",
		"HDMI-A-2": "",
		"DP-1":     "",
	})
}

func TestMonitorWaitReturnsTheMonitorThatIsAlreadyThere(t *testing.T) {
	// A card with a monitor on it starts the compositor with no wait
	// at all, so the event in the channel must still be there after.
	events := make(chan drmEvent, 1)
	events <- drmEvent{Action: "change", DevPath: cardPath}

	output, err := waitForMonitor(context.Background(), labSysfs(t), "card1", events)
	if err != nil {
		t.Fatal(err)
	}
	if output.Connector != "HDMI-A-1" {
		t.Errorf("the wait returned %q", output.Connector)
	}
	if len(events) != 1 {
		t.Errorf("the channel holds %d events, want 1", len(events))
	}
}

// monitorWait is one wait under test. The events channel is
// unbuffered, so a send returns only after the wait has read the
// event, and a wait that has read an event has already made the sysfs
// read the event before it asked for.
type monitorWait struct {
	events chan drmEvent
	found  chan Output
}

// monitorWaitDeadline is how long a test waits for the wait to
// answer. Nothing here takes this long when the code is right; the
// deadline names a wait that never ended instead of hanging the run.
const monitorWaitDeadline = 5 * time.Second

// startMonitorWait starts a wait over the tree and sends it one
// event. The send returns only after the wait has read every
// connector and blocked on the channel, so the test that follows
// knows its own fixture change comes after that first read.
func startMonitorWait(t *testing.T, sysRoot, card string) *monitorWait {
	t.Helper()
	wait := &monitorWait{events: make(chan drmEvent), found: make(chan Output, 1)}
	go func() {
		output, err := waitForMonitor(context.Background(), sysRoot, card, wait.events)
		if err != nil {
			t.Errorf("the wait failed: %v", err)
		}
		wait.found <- output
	}()
	wait.send(t, drmEvent{Action: "change", DevPath: cardPath})
	return wait
}

// send delivers one event, or accepts that the wait already ended: a
// monitor the wait read from sysfs before this event ends it, and
// then nobody reads the event. The result goes back into found for
// the assertion to take.
func (w *monitorWait) send(t *testing.T, event drmEvent) {
	t.Helper()
	select {
	case w.events <- event:
	case output := <-w.found:
		w.found <- output
	case <-time.After(monitorWaitDeadline):
		t.Fatalf("the wait neither read the %s event nor ended", event.Action)
	}
}

// monitor takes the output the wait ended on, or fails the test at
// the deadline.
func (w *monitorWait) monitor(t *testing.T) Output {
	t.Helper()
	select {
	case output := <-w.found:
		return output
	case <-time.After(monitorWaitDeadline):
		t.Fatal("the wait did not end after a monitor arrived")
	}
	return Output{}
}

func TestMonitorWaitEndsWhenAMonitorArrives(t *testing.T) {
	// The quiet events are the kernel's drm events for changes that
	// leave every connector dark. The wait re-reads sysfs and blocks
	// again on each one.
	cases := []struct {
		name  string
		quiet []drmEvent
	}{
		{
			name: "the next event carries the monitor",
		},
		{
			name: "events that change nothing come first",
			quiet: []drmEvent{
				{Action: "change", DevPath: cardPath},
				{Action: "add", DevPath: cardPath},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := darkSysfs(t)
			wait := startMonitorWait(t, root, "card1")
			for _, event := range c.quiet {
				wait.send(t, event)
			}

			writeConnector(t, root, "card1", "DP-1", "portable-display")
			wait.send(t, drmEvent{Action: "change", DevPath: cardPath})

			output := wait.monitor(t)
			if output.Connector != "DP-1" {
				t.Errorf("the wait returned %q", output.Connector)
			}
			if !output.Connected || output.Monitor.WidthPixels != 1920 {
				t.Errorf("the wait returned %+v", output)
			}
		})
	}
}

func TestMonitorWaitEndsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := waitForMonitor(ctx, darkSysfs(t), "card1", make(chan drmEvent))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("the wait ended with %v", err)
	}
}

func TestMonitorWaitEndsWhenTheListenerStops(t *testing.T) {
	// A closed channel delivers a zero value forever, so a wait that
	// read it as an event would spin.
	events := make(chan drmEvent)
	close(events)

	_, err := waitForMonitor(context.Background(), darkSysfs(t), "card1", events)
	if err == nil {
		t.Fatal("the listener stopped and the wait reported nothing")
	}
}
