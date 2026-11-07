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
				hold := newHotplugHold(nil)
				for _, output := range c.before {
					hold.apply([]Output{output})
				}
				hold.apply([]Output{darkOutput("HDMI-A-1")})
				time.Sleep(c.dark)

				got := hold.apply([]Output{darkOutput("HDMI-A-1")})[0]

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
		hold := newHotplugHold(nil)
		lit := litOutput("HDMI-A-1", labMonitor())
		hold.apply([]Output{lit})
		hold.apply([]Output{darkOutput("HDMI-A-1")})
		time.Sleep(hotplugSettle - time.Second)
		hold.apply([]Output{lit})
		hold.apply([]Output{darkOutput("HDMI-A-1")})
		time.Sleep(hotplugSettle - time.Second)

		if got := hold.apply([]Output{darkOutput("HDMI-A-1")})[0]; !got.Held {
			t.Error("the second drop was not held, though it is shorter than the settle time")
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
				hold := newHotplugHold(func() { wakes.Add(1) })
				lit := litOutput("HDMI-A-1", labMonitor())
				hold.apply([]Output{lit})
				hold.apply([]Output{darkOutput("HDMI-A-1")})
				hold.apply([]Output{darkOutput("HDMI-A-1")})
				if c.back {
					hold.apply([]Output{lit})
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

// A nil hold answers the walk as it is, which is what a caller that
// holds nothing passes.
func TestANilHoldAnswersTheWalk(t *testing.T) {
	var hold *hotplugHold
	if got := hold.apply([]Output{darkOutput("HDMI-A-1")}); got[0].Connected {
		t.Errorf("a nil hold answered %+v", got[0])
	}
}
