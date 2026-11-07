package main

// The hold on a connector whose hot plug detect drops for a moment.
// Each test runs in a synctest bubble, so the settle time passes on the
// bubble's clock and the deadline timer fires on it.

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// darkOutput is a connector that reads disconnected, with nothing in
// its EDID file, the way sysfs answers during a drop.
func darkOutput(connector string) Output {
	return Output{Connector: connector}
}

// A connector that goes dark answers its last lit reading until it has
// stayed dark for the settle time, and answers dark from then on. A
// connector that this process never saw lit answers dark at once,
// because there is no reading to hold.
func TestAConnectorThatGoesDarkIsHeldForTheSettleTime(t *testing.T) {
	lit := litOutput("HDMI-A-1", labMonitor())
	lit.CurrentMode = "1920x1080@60"
	cases := []struct {
		name      string
		before    []Output
		dark      time.Duration
		connected bool
	}{
		{"a drop shorter than the settle time", []Output{lit}, hotplugSettle - time.Millisecond, true},
		{"a drop as long as the settle time", []Output{lit}, hotplugSettle, false},
		{"a connector never seen lit", nil, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				view := newHotplugHold(nil, nil).view()
				for _, output := range c.before {
					view.apply([]Output{output})
				}
				view.apply([]Output{darkOutput("HDMI-A-1")})
				time.Sleep(c.dark)

				got := view.apply([]Output{darkOutput("HDMI-A-1")})[0]

				if got.Connected != c.connected || got.Held != c.connected {
					t.Fatalf("the connector reads connected=%v held=%v, want %v", got.Connected, got.Held, c.connected)
				}
				if c.connected && (got.Monitor != lit.Monitor || got.CurrentMode != lit.CurrentMode) {
					t.Errorf("the held connector answers %+v, want the lit reading %+v", got, lit)
				}
			})
		})
	}
}

// A connector that comes back inside the settle time ends the hold, so
// the next drop is held for a whole settle time of its own.
func TestEachDropIsHeldForItsOwnSettleTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		view := newHotplugHold(nil, nil).view()
		lit := litOutput("HDMI-A-1", labMonitor())
		view.apply([]Output{lit})
		view.apply([]Output{darkOutput("HDMI-A-1")})
		time.Sleep(hotplugSettle - time.Second)
		view.apply([]Output{lit})
		view.apply([]Output{darkOutput("HDMI-A-1")})
		time.Sleep(hotplugSettle - time.Second)

		if got := view.apply([]Output{darkOutput("HDMI-A-1")})[0]; !got.Held {
			t.Error("the second drop was not held, though it is shorter than the settle time")
		}
	})
}

// A burst of drops that no pass reads lit between is still a burst of
// short drops. A hotplug event between two dark reads means the
// connector moved since the last one, so the hold starts again from
// the later read. With no event between them, the connector stayed
// dark, and the hold ends at the settle time.
func TestAHotplugEventBetweenDarkReadsStartsTheHoldAgain(t *testing.T) {
	cases := []struct {
		name  string
		event bool
		held  bool
	}{
		{"an event between the reads", true, true},
		{"no event between the reads", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var hotplugs atomic.Uint64
				view := newHotplugHold(nil, hotplugs.Load).view()
				view.apply([]Output{litOutput("HDMI-A-1", labMonitor())})
				view.apply([]Output{darkOutput("HDMI-A-1")})
				time.Sleep(hotplugSettle - time.Second)
				if c.event {
					hotplugs.Add(2)
				}
				view.apply([]Output{darkOutput("HDMI-A-1")})
				time.Sleep(2 * time.Second)

				if got := view.apply([]Output{darkOutput("HDMI-A-1")})[0]; got.Held != c.held {
					t.Errorf("the connector reads held=%v, want %v", got.Held, c.held)
				}
			})
		})
	}
}

// The readers of one hold share each connector's dark time, so the
// Display, the windows, and the slice report a monitor that left at
// the same deadline, whichever reader saw the drop first.
func TestEveryViewOfAHoldEndsItAtOneDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hold := newHotplugHold(nil, nil)
		screens, slice := hold.view(), hold.view()
		lit := litOutput("HDMI-A-1", labMonitor())
		screens.apply([]Output{lit})
		slice.apply([]Output{lit})
		screens.apply([]Output{darkOutput("HDMI-A-1")})
		time.Sleep(hotplugSettle - time.Second)
		slice.apply([]Output{darkOutput("HDMI-A-1")})
		time.Sleep(time.Second)

		if got := slice.apply([]Output{darkOutput("HDMI-A-1")})[0]; got.Held {
			t.Error("the slice still holds a connector the other view read dark a settle time ago")
		}
	})
}

// A monitor that answers on the connector is never shown as the one
// held: a different monitor that arrives inside the settle time is read
// as it is.
func TestADifferentMonitorEndsTheHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		view := newHotplugHold(nil, nil).view()
		view.apply([]Output{litOutput("HDMI-A-1", labMonitor())})
		view.apply([]Output{darkOutput("HDMI-A-1")})

		got := view.apply([]Output{litOutput("HDMI-A-1", portableMonitor())})[0]

		if got.Held || got.Monitor != portableMonitor() {
			t.Errorf("the connector answers %+v, want the monitor on the wire", got)
		}
	})
}

// The end of a hold wakes a pass once, so a monitor that really left
// is reported at the deadline and not on the next unrelated event. A
// drop that ends inside the settle time wakes nothing extra.
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
				lit := litOutput("HDMI-A-1", labMonitor())
				view.apply([]Output{lit})
				view.apply([]Output{darkOutput("HDMI-A-1")})
				view.apply([]Output{darkOutput("HDMI-A-1")})
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

// The operator's own wiring: the end of a hold schedules one more pass
// through the channel a failed write retries on, and the loop's wakes
// carry it to the settled pass.
func TestTheEndOfAHoldReachesTheLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		retries := make(chan struct{}, 1)
		hold := newCardHold(retries, nil)
		out := wakes(t.Context(), nil, retries, nil, nil, nil, nil)
		view := hold.view()
		view.apply([]Output{litOutput("HDMI-A-1", labMonitor())})
		view.apply([]Output{darkOutput("HDMI-A-1")})

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
		var count atomic.Uint64
		in := make(chan drmEvent, 1)
		out := countHotplugs(t.Context(), in, &count)

		in <- drmEvent{Action: "change", DevPath: "/devices/drm/card1"}
		<-out

		if got := count.Load(); got != 1 {
			t.Errorf("the count is %d after one event, want 1", got)
		}
	})
}

// A nil view answers the walk as it is, which is what a caller that
// holds nothing passes.
func TestANilViewAnswersTheWalk(t *testing.T) {
	var view *hotplugView
	if got := view.apply([]Output{darkOutput("HDMI-A-1")}); got[0].Connected {
		t.Errorf("a nil view answered %+v", got[0])
	}
}
