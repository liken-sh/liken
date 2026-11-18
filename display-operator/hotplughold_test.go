package main

// Each test runs in a synctest bubble, so the settle time passes on the
// bubble's clock and the deadline timer fires on it.

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// A connector with no connected reading is returned disconnected at
// once, because there is nothing to hold.
func TestADisconnectedConnectorIsHeldForTheSettleTime(t *testing.T) {
	lit := connectedOutput("HDMI-A-1", labMonitor())
	lit.CurrentMode = "1920x1080@60"
	cases := []struct {
		name      string
		before    []Output
		dark      time.Duration
		connected bool
	}{
		{"a drop shorter than the settle time", []Output{lit}, hotplugSettle - time.Millisecond, true},
		{"a drop as long as the settle time", []Output{lit}, hotplugSettle, false},
		{"a connector never seen connected", nil, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				view := newHotplugHold(nil, nil).view()
				for _, output := range c.before {
					view.apply([]Output{output})
				}
				view.apply([]Output{disconnectedOutput("HDMI-A-1")})
				time.Sleep(c.dark)

				got := view.apply([]Output{disconnectedOutput("HDMI-A-1")})[0]

				if got.Connected != c.connected || got.Held != c.connected {
					t.Fatalf("the connector reads connected=%v held=%v, want %v", got.Connected, got.Held, c.connected)
				}
				if c.connected && (got.Monitor != lit.Monitor || got.CurrentMode != lit.CurrentMode) {
					t.Errorf("the held connector reads %+v, want the connected reading %+v", got, lit)
				}
			})
		})
	}
}

// A connector that comes back before the deadline ends the hold, so
// the next drop gets a whole settle time of its own.
func TestEachDropIsHeldForItsOwnSettleTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		view := newHotplugHold(nil, nil).view()
		lit := connectedOutput("HDMI-A-1", labMonitor())
		view.apply([]Output{lit})
		view.apply([]Output{disconnectedOutput("HDMI-A-1")})
		time.Sleep(hotplugSettle - time.Second)
		view.apply([]Output{lit})
		view.apply([]Output{disconnectedOutput("HDMI-A-1")})
		time.Sleep(hotplugSettle - time.Second)

		if got := view.apply([]Output{disconnectedOutput("HDMI-A-1")})[0]; !got.Held {
			t.Error("the second drop was not held, though it is shorter than the settle time")
		}
	})
}

// When no pass reads the connector connected between drops, a burst of
// short drops looks like one long outage, so a uevent that may concern
// the connector restarts the hold from the later read.
func TestAHotplugEventBetweenDisconnectedReadsRestartsTheHold(t *testing.T) {
	cases := []struct {
		name   string
		events []drmEvent
		held   bool
	}{
		{"an event that names no connector", []drmEvent{{Action: "change", DevPath: cardPath}}, true},
		{"an event that names this connector", []drmEvent{{Action: "change", DevPath: cardPath, Connector: "403"}}, true},
		{"an event that names another connector", []drmEvent{{Action: "change", DevPath: cardPath, Connector: "409"}}, false},
		{"an event of another card", []drmEvent{{Action: "change", DevPath: otherCardPath, Connector: "403"}}, false},
		{"an event the socket lost", []drmEvent{{Action: "lost events", DevPath: "the uevent socket reported ENOBUFS"}}, true},
		{"no event between the reads", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				counts := newHotplugCounts("card1")
				view := newHotplugHold(nil, counts.count).view()
				view.apply([]Output{numbered(connectedOutput("HDMI-A-1", labMonitor()))})
				view.apply([]Output{numbered(disconnectedOutput("HDMI-A-1"))})
				time.Sleep(hotplugSettle - time.Second)
				for _, event := range c.events {
					counts.add(event)
				}
				view.apply([]Output{numbered(disconnectedOutput("HDMI-A-1"))})
				time.Sleep(2 * time.Second)

				if got := view.apply([]Output{numbered(disconnectedOutput("HDMI-A-1"))})[0]; got.Held != c.held {
					t.Errorf("the connector reads held=%v, want %v", got.Held, c.held)
				}
			})
		})
	}
}

// otherCardPath is a second GPU on the same machine, whose events the
// uevent socket also receives.
const otherCardPath = "/devices/pci0000:00/0000:03:00.0/drm/card0"

// numbered sets the DRM object id that the uevents in these tests use
// for HDMI-A-1.
func numbered(output Output) Output {
	output.ConnectorID = "403"
	return output
}

// A neighbour on the same card that drops every 3 seconds sends uevents
// that name no connector. They delay the report of an unplug here to
// at most hotplugCeiling plus hotplugSettle, and never hide it.
func TestAFlappingNeighbourDelaysAnUnplugOnlyToTheCeiling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		counts := newHotplugCounts("card1")
		view := newHotplugHold(nil, counts.count).view()
		view.apply([]Output{connectedOutput("HDMI-A-1", labMonitor())})
		dark := []Output{disconnectedOutput("HDMI-A-1")}
		view.apply(dark)
		reported := time.Duration(0)
		for elapsed := 3 * time.Second; elapsed <= hotplugCeiling+2*hotplugSettle; elapsed += 3 * time.Second {
			time.Sleep(3 * time.Second)
			counts.add(drmEvent{Action: "change", DevPath: cardPath})
			if got := view.apply(dark)[0]; !got.Held && reported == 0 {
				reported = elapsed
			}
		}

		if reported == 0 || reported > hotplugCeiling+hotplugSettle {
			t.Errorf("the unplug was reported %v after it, want by %v", reported, hotplugCeiling+hotplugSettle)
		}
	})
}

// The deadline runs from the first view that read the drop.
func TestEveryViewOfAHoldEndsItAtTheSameDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hold := newHotplugHold(nil, nil)
		screens, slice := hold.view(), hold.view()
		lit := connectedOutput("HDMI-A-1", labMonitor())
		screens.apply([]Output{lit})
		slice.apply([]Output{lit})
		screens.apply([]Output{disconnectedOutput("HDMI-A-1")})
		time.Sleep(hotplugSettle - time.Second)
		slice.apply([]Output{disconnectedOutput("HDMI-A-1")})
		time.Sleep(time.Second)

		if got := slice.apply([]Output{disconnectedOutput("HDMI-A-1")})[0]; got.Held {
			t.Error("the slice still holds a connector the other view read disconnected a settle time ago")
		}
	})
}

// A different monitor that arrives during a hold is returned as read.
func TestADifferentMonitorEndsTheHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		view := newHotplugHold(nil, nil).view()
		view.apply([]Output{connectedOutput("HDMI-A-1", labMonitor())})
		view.apply([]Output{disconnectedOutput("HDMI-A-1")})

		got := view.apply([]Output{connectedOutput("HDMI-A-1", portableMonitor())})[0]

		if got.Held || got.Monitor != portableMonitor() {
			t.Errorf("the connector reads %+v, want the connected monitor", got)
		}
	})
}

// A drop that ends before the deadline wakes no pass.
func TestTheEndOfAHoldWakesOnePass(t *testing.T) {
	cases := []struct {
		name  string
		back  bool
		wakes int64
	}{
		{"a monitor that stays away", false, 1},
		{"a monitor that comes back", true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var wakes atomic.Int64
				view := newHotplugHold(func() { wakes.Add(1) }, nil).view()
				lit := connectedOutput("HDMI-A-1", labMonitor())
				view.apply([]Output{lit})
				view.apply([]Output{disconnectedOutput("HDMI-A-1")})
				view.apply([]Output{disconnectedOutput("HDMI-A-1")})
				if c.back {
					view.apply([]Output{lit})
				}

				time.Sleep(2 * hotplugSettle)
				synctest.Wait()

				if got := wakes.Load(); got != c.wakes {
					t.Errorf("the hold woke %d passes, want %d", got, c.wakes)
				}
			})
		})
	}
}

// The card's hold sends on the retries channel at its deadline, and
// wakes turns that send into a pass of the loop.
func TestTheEndOfACardHoldWakesTheLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		retries := make(chan struct{}, 1)
		hold := newCardHold(retries, nil)
		out := wakes(t.Context(), nil, retries, nil, nil, nil, nil)
		view := hold.view()
		view.apply([]Output{connectedOutput("HDMI-A-1", labMonitor())})
		view.apply([]Output{disconnectedOutput("HDMI-A-1")})

		time.Sleep(hotplugSettle)
		synctest.Wait()

		if !woke(out) {
			t.Error("the end of the hold woke no pass")
		}
	})
}

// Each drm event is counted before it reaches the loop, so the pass it
// wakes reads a count that includes it.
func TestEachDRMEventIsCountedBeforeItWakesThePass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		counts := newHotplugCounts("card1")
		in := make(chan drmEvent, 1)
		out := countHotplugs(t.Context(), in, counts)

		in <- drmEvent{Action: "change", DevPath: cardPath, Connector: "403"}
		<-out

		if got := counts.count("403"); got != 1 {
			t.Errorf("the count is %d after one event, want 1", got)
		}
	})
}

func TestANilViewReturnsTheReadUnchanged(t *testing.T) {
	var view *hotplugView
	if got := view.apply([]Output{disconnectedOutput("HDMI-A-1")}); got[0].Connected {
		t.Errorf("a nil view returns the connector as %+v, want it unchanged and disconnected", got[0])
	}
}
