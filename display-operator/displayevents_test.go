package main

// The Events a person reads with `kubectl describe display`: one for
// each condition transition after its status write lands, one for each
// write the panel did not confirm, and one for each action the
// operator took on a screen. Each test runs in a synctest bubble, so
// synctest.Wait lets the recorder's goroutine write its queue before
// the test reads the fake API server's Events.

import (
	"fmt"
	"slices"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
)

// eventLines answers each Event as its type, reason, and message, the
// three columns a person reads in `kubectl describe`.
func eventLines(held []events.Event) []string {
	var lines []string
	for _, event := range held {
		lines = append(lines, event.Type+" "+event.Reason+" "+event.Message)
	}
	return lines
}

// A panel that arrives posts one Event for each condition its first
// status write states, a pass that changes nothing posts nothing, and
// the panel that leaves posts the Connected transition. Both are
// Normal, because a person plugs and unplugs a monitor.
func TestAPanelThatArrivesAndLeavesPostsItsTransitions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))

		if err := fixture.pass(); err != nil {
			t.Fatal(err)
		}
		fixture.passes(2)
		fixture.present["HDMI-A-1"] = false
		fixture.passes(2)
		synctest.Wait()

		want := []string{
			"Normal PanelAttached HDMI-A-1 carries this panel",
			"Normal AnswersDDC the panel answers DDC/CI",
			"Normal NoPanel no panel on HDMI-A-1",
		}
		if got := eventLines(fixture.events.About("Display", labDisplayName())); !slices.Equal(got, want) {
			t.Errorf("the Events are\n%q\nwant\n%q", got, want)
		}
	})
}

// A status write the API server refuses posts nothing, and the pass
// that writes the same status later posts its transitions once.
func TestARefusedStatusWritePostsNoEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
		fixture.declare(DisplaySpec{})
		fixture.refuse = 1

		_ = fixture.pass()
		synctest.Wait()
		if got := fixture.events.List(); len(got) != 0 {
			t.Fatalf("a refused write posted %q, want nothing", eventLines(got))
		}

		fixture.passes(2)
		synctest.Wait()
		want := []string{
			"Normal PanelAttached HDMI-A-1 carries this panel",
			"Normal AnswersDDC the panel answers DDC/CI",
		}
		if got := eventLines(fixture.events.About("Display", labDisplayName())); !slices.Equal(got, want) {
			t.Errorf("the Events are\n%q\nwant\n%q", got, want)
		}
	})
}

// A write the panel reads back differently is recorded once in
// status, and posts one Warning with the value and the readback,
// however many passes follow.
func TestAWriteThePanelDidNotConfirmPostsOneWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		panel := drillPanel(t, "lg-hdr-wqhd")
		panel.clamps[vcpInput] = 0x0f
		fixture := newDisplayFixture(t, panel)
		fixture.declare(DisplaySpec{Input: stringOf("HDMI-1")})

		fixture.passes(4)
		synctest.Wait()

		var warnings []string
		for _, event := range fixture.events.About("Display", labDisplayName()) {
			if event.Reason == WriteUnconfirmedReason {
				warnings = append(warnings, event.Type+" "+event.Message)
			}
		}
		entry := fixture.display().Status.Unconfirmed[0]
		want := []string{fmt.Sprintf("Warning the operator wrote input HDMI-1 and the device did not confirm it; it reads back DP-1: %s. "+
			"The operator does not write it again until spec changes", entry.Message)}
		if !slices.Equal(warnings, want) {
			t.Errorf("the WriteUnconfirmed Events are\n%q\nwant\n%q", warnings, want)
		}
	})
}

// Each condition a pass writes through the store posts its
// transition, and the reason decides the type: a fault a person must
// fix is a Warning, and a state a person causes is Normal.
func TestATransitionIsAWarningOnlyForAFault(t *testing.T) {
	cases := []struct {
		condition string
		status    bool
		reason    string
		want      string
	}{
		{LayoutResolvedCondition, false, LayoutNotFoundReason, events.TypeWarning},
		{LayoutResolvedCondition, true, LayoutFoundReason, events.TypeNormal},
		{CompositorServingCondition, false, CompositorDownReason, events.TypeWarning},
		{CompositorServingCondition, false, CompositorHungReason, events.TypeWarning},
		{CompositorServingCondition, true, CompositorServingReason, events.TypeNormal},
		{PhysicalAddressCurrentCondition, false, AmbiguousReason, events.TypeWarning},
		{PhysicalAddressCurrentCondition, false, RetainedReason, events.TypeNormal},
		{ConnectedCondition, false, NoPanelReason, events.TypeNormal},
		{ResponsiveCondition, false, NoDDCReplyReason, events.TypeNormal},
	}
	for _, c := range cases {
		t.Run(c.reason, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
				display := fixture.captured(labDisplayName(), "HDMI-A-1", DisplayValues{})
				next := fixture.control.condition(c.condition, c.status, c.reason, "the message")

				err := fixture.control.displays.settleStatus(display, func(published DisplayStatus) (DisplayStatus, bool) {
					published.Conditions = setCondition(published.Conditions, next)
					return published, true
				})
				if err != nil {
					t.Fatal(err)
				}
				synctest.Wait()

				want := []string{c.want + " " + c.reason + " the message"}
				if got := eventLines(fixture.events.About("Display", labDisplayName())); !slices.Equal(got, want) {
					t.Errorf("the Events are %q, want %q", got, want)
				}
			})
		})
	}
}

// screensOfTwoNodes is a fixture whose API server holds two Displays of
// this node and one of another node, and the notices of this node over
// its store.
func screensOfTwoNodes(t *testing.T) (*displayFixture, *screenNotices) {
	t.Helper()
	fixture := newDisplayFixture(t, drillPanel(t, "lg-hdr-wqhd"))
	fixture.captured("living-room", "HDMI-A-1", DisplayValues{})
	fixture.captured("desk", "HDMI-A-2", DisplayValues{})
	elsewhere := fixture.captured("kitchen", "HDMI-A-1", DisplayValues{})
	elsewhere.Status.Node = "node-2"
	notices := &screenNotices{node: "liken-1"}
	notices.displays.Store(fixture.control.displays)
	return fixture, notices
}

// A notice names a connector, and goes on the Display of this node
// that the connector serves. A notice with no connector goes on every
// Display of this node, and never on another node's.
func TestANoticeGoesOnTheScreensOfThisNode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture, notices := screensOfTwoNodes(t)

		notices.post("HDMI-A-2", events.TypeNormal, ModeChangedReason, "one screen")
		notices.post("", events.TypeWarning, CompositorKilledReason, "every screen")
		synctest.Wait()

		got := map[string][]string{}
		for _, name := range []string{"living-room", "desk", "kitchen"} {
			got[name] = eventLines(fixture.events.About("Display", name))
		}
		want := map[string][]string{
			"living-room": {"Warning CompositorKilled every screen"},
			"desk":        {"Normal ModeChanged one screen", "Warning CompositorKilled every screen"},
			"kitchen":     nil,
		}
		for name := range want {
			if !slices.Equal(got[name], want[name]) {
				t.Errorf("%s has the Events %q, want %q", name, got[name], want[name])
			}
		}
	})
}

// Notices before the Display store is set post nothing, and so do nil
// notices, which is a plugin built with none.
func TestNoticesWithNoStorePostNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture, _ := screensOfTwoNodes(t)
		var none *screenNotices

		(&screenNotices{node: "liken-1"}).post("", events.TypeNormal, ModeChangedReason, "no store")
		none.post("", events.TypeNormal, ModeChangedReason, "no notices")
		synctest.Wait()

		if got := fixture.events.List(); len(got) != 0 {
			t.Errorf("the notices posted %q, want nothing", eventLines(got))
		}
	})
}

// A compositor that answered nothing and was ended posts a Warning on
// every screen of the node, because every screen on the card blanks.
// A kill that found no compositor posts nothing.
func TestAHungCompositorThatWasEndedPostsOnEveryScreen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture, notices := screensOfTwoNodes(t)
		plugin := &draPlugin{
			compositors: func() []int { return []int{14} },
			signal:      func(int, syscall.Signal) error { return nil },
			notices:     notices,
		}
		missing := &draPlugin{compositors: func() []int { return nil }, notices: notices}

		if err := plugin.killHungCompositor(); err != nil {
			t.Fatal(err)
		}
		_ = missing.killHungCompositor()
		synctest.Wait()

		want := []string{"Warning CompositorKilled the compositor answered nothing for " + compositorHungLimit.String() +
			"; the operator ended it, and it starts again"}
		for _, name := range []string{"living-room", "desk"} {
			if got := eventLines(fixture.events.About("Display", name)); !slices.Equal(got, want) {
				t.Errorf("%s has the Events %q, want %q", name, got, want)
			}
		}
	})
}

// A panel that does not take the standby after its last claim ended
// stays on, and the Warning says so on its Display.
func TestAStandbyThatFailedPostsAWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture, notices := screensOfTwoNodes(t)
		controls, _ := benchPanels(t, t.TempDir(), "card1", nil)
		plugin := &draPlugin{controls: controls, powerPath: t.TempDir() + "/power.json", notices: notices}
		if err := writePowerRecord(plugin.powerPath, map[string]string{"HDMI-A-2": powerReleased}); err != nil {
			t.Fatal(err)
		}

		plugin.standbyReleased("HDMI-A-2")
		synctest.Wait()

		got := fixture.events.About("Display", "desk")
		if len(got) != 1 || got[0].Type != events.TypeWarning || got[0].Reason != PanelStandbyFailedReason {
			t.Errorf("desk has the Events %q, want one PanelStandbyFailed Warning", eventLines(got))
		}
	})
}

// A mode switch that restarts the compositor posts ModeChanged on the
// screen whose mode changed, with the mode before and after.
func TestAModeSwitchPostsModeChanged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture, notices := screensOfTwoNodes(t)
		plugin := modeSwitchPlugin(t)
		plugin.notices = notices
		portable := Output{Connector: "HDMI-A-2"}

		if err := plugin.applyMode(t.Context(), portable, "1280x720"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()

		want := []string{"Normal ModeChanged the mode of HDMI-A-2 goes from 1920x1080@60 to 1280x720; " +
			"the compositor restarts, and every screen on the card blanks"}
		if got := eventLines(fixture.events.About("Display", "desk")); !slices.Equal(got, want) {
			t.Errorf("desk has the Events %q, want %q", got, want)
		}
	})
}

// modeSwitchPlugin is the lab's plugin with only the seams a mode
// switch acts through: the fake compositor, and no layout module, so
// the switch runs in a synctest bubble.
func modeSwitchPlugin(t *testing.T) *draPlugin {
	t.Helper()
	plugin, _ := modeSwitchBench(t)
	return plugin
}

// modeSwitchBench is the same plugin and the fake compositor behind
// it, for a test that changes how the compositor comes back.
func modeSwitchBench(t *testing.T) (*draPlugin, *fakeCompositor) {
	t.Helper()
	configDir := t.TempDir()
	compositor := &fakeCompositor{
		record:  configDir + "/modes.json",
		current: map[string]string{"HDMI-A-1": "3840x1600@60", "HDMI-A-2": "1920x1080@60"},
		offers:  labConnectorModes(),
	}
	return &draPlugin{
		configPath:     configDir + "/weston.ini",
		recordPath:     compositor.record,
		currentModes:   compositor.modes,
		connectorModes: compositor.connectors,
		compositors:    compositor.pids,
		signal:         compositor.signal,
		served:         compositor.serving,
		republish:      compositor.republish,
		switchTimeout:  time.Second,
		switchFallback: time.Millisecond,
		metrics:        newMetrics(componentName, "dev"),
	}, compositor
}
