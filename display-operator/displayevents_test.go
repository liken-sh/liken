package main

// The Events that `kubectl describe display` shows: one for each
// condition transition after its status write succeeds, one for each
// write the panel did not confirm, and one for each action the operator
// takes on a screen. Each test runs in a synctest bubble, so
// synctest.Wait lets the recorder's goroutine write its queue before
// the test reads the Events from the fake API server.

import (
	"fmt"
	"slices"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
)

// eventLines returns each Event as its type, reason, and message, the
// columns that `kubectl describe` shows.
func eventLines(recorded []events.Event) []string {
	var lines []string
	for _, event := range recorded {
		lines = append(lines, event.Type+" "+event.Reason+" "+event.Message)
	}
	return lines
}

// A panel that arrives posts the transitions of its first status write,
// a pass that changes nothing posts nothing, and a panel that leaves
// posts NoPanel. All are Normal, because a person plugs and unplugs
// monitors.
func TestAPanelThatArrivesAndLeavesPostsItsTransitions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))

		if err := fixture.pass(); err != nil {
			t.Fatalf("running a pass: %v", err)
		}
		fixture.passes(2)
		fixture.present["HDMI-A-1"] = false
		fixture.passes(2)
		synctest.Wait()

		want := []string{
			"Normal PanelAttached The panel is connected to HDMI-A-1",
			"Normal AnswersDDC The panel responds to DDC/CI",
			"Normal NoPanel No panel is connected to HDMI-A-1",
		}
		if got := eventLines(fixture.events.About("Display", labDisplayName())); !slices.Equal(got, want) {
			t.Errorf("the Events are\n%q\nwant\n%q", got, want)
		}
	})
}

// A status write that the API server refuses posts nothing. A later
// pass that writes the same status posts its transitions once.
func TestARefusedStatusWritePostsNoEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
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
			"Normal PanelAttached The panel is connected to HDMI-A-1",
			"Normal AnswersDDC The panel responds to DDC/CI",
		}
		if got := eventLines(fixture.events.About("Display", labDisplayName())); !slices.Equal(got, want) {
			t.Errorf("the Events are\n%q\nwant\n%q", got, want)
		}
	})
}

// A write that the panel reads back with a different value is recorded
// once in the status and posts one Warning with the written value and
// the readback. Later passes post nothing more.
func TestAWriteThePanelDidNotConfirmPostsOneWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		panel := recordedPanel(t, "lg-hdr-wqhd")
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
		want := []string{fmt.Sprintf("Warning The device did not confirm the write of input HDMI-1, and it reports DP-1: %s. "+
			"The operator does not write the value again until spec changes", entry.Message)}
		if !slices.Equal(warnings, want) {
			t.Errorf("the WriteUnconfirmed Events are\n%q\nwant\n%q", warnings, want)
		}
	})
}

// The reason decides the type of a transition's Event: a fault that a
// person must fix is a Warning, and a state that a person caused is
// Normal.
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
				fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
				display := fixture.captured(labDisplayName(), "HDMI-A-1", DisplayValues{})
				next := fixture.control.condition(c.condition, c.status, c.reason, "the message")

				err := fixture.control.displays.settleStatus(display, func(published DisplayStatus) (DisplayStatus, bool) {
					published.Conditions = setCondition(published.Conditions, next)
					return published, true
				})
				if err != nil {
					t.Fatalf("publishing the Display status: %v", err)
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

// screensOfTwoNodes returns a fixture whose fake API server has two
// Displays of this node and one Display of another node, with a
// screenNotices for this node that uses the fixture's store.
func screensOfTwoNodes(t *testing.T) (*displayFixture, *screenNotices) {
	t.Helper()
	fixture := newDisplayFixture(t, recordedPanel(t, "lg-hdr-wqhd"))
	fixture.captured("screen-a", "HDMI-A-1", DisplayValues{})
	fixture.captured("screen-b", "HDMI-A-2", DisplayValues{})
	elsewhere := fixture.captured("screen-c", "HDMI-A-1", DisplayValues{})
	elsewhere.Status.Node = "node-2"
	notices := &screenNotices{node: "liken-1"}
	notices.displays.Store(fixture.control.displays)
	return fixture, notices
}

// A notice with a connector goes on the Display of this node that has
// that connector. A notice with no connector goes on every Display of
// this node, and never on a Display of another node.
func TestANoticeGoesOnTheScreensOfThisNode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture, notices := screensOfTwoNodes(t)

		notices.post("HDMI-A-2", events.TypeNormal, ModeChangedReason, "one screen")
		notices.post("", events.TypeWarning, CompositorKilledReason, "every screen")
		synctest.Wait()

		got := map[string][]string{}
		for _, name := range []string{"screen-a", "screen-b", "screen-c"} {
			got[name] = eventLines(fixture.events.About("Display", name))
		}
		want := map[string][]string{
			"screen-a": {"Warning CompositorKilled every screen"},
			"screen-b": {"Normal ModeChanged one screen", "Warning CompositorKilled every screen"},
			"screen-c": nil,
		}
		for name := range want {
			if !slices.Equal(got[name], want[name]) {
				t.Errorf("%s has the Events %q, want %q", name, got[name], want[name])
			}
		}
	})
}

// A screenNotices whose store is not set posts nothing, and neither
// does the nil *screenNotices of a plugin built without notices.
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

// A compositor that did not respond and was killed posts a Warning on
// every screen of the node, because every screen on the GPU goes blank.
// A kill that finds no compositor posts nothing.
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
			t.Fatalf("killing the hung compositor: %v", err)
		}
		_ = missing.killHungCompositor()
		synctest.Wait()

		want := []string{"Warning CompositorKilled Ended the compositor, which did not reply for " + compositorHungLimit.String() +
			". Its container starts it again"}
		for _, name := range []string{"screen-a", "screen-b"} {
			if got := eventLines(fixture.events.About("Display", name)); !slices.Equal(got, want) {
				t.Errorf("%s has the Events %q, want %q", name, got, want)
			}
		}
	})
}

// A panel that fails to go to standby after its last claim ended posts
// a Warning.
func TestAStandbyThatFailedPostsAWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture, notices := screensOfTwoNodes(t)
		controls, _ := benchPanels(t, t.TempDir(), "card1", nil)
		plugin := &draPlugin{controls: controls, powerPath: t.TempDir() + "/power.json", notices: notices}
		if err := writePowerRecord(plugin.powerPath, map[string]string{"HDMI-A-2": powerReleased}); err != nil {
			t.Fatalf("writing the power record: %v", err)
		}

		plugin.standbyReleased("HDMI-A-2")
		synctest.Wait()

		got := fixture.events.About("Display", "screen-b")
		if len(got) != 1 || got[0].Type != events.TypeWarning || got[0].Reason != PanelStandbyFailedReason {
			t.Errorf("screen-b has the Events %q, want one PanelStandbyFailed Warning", eventLines(got))
		}
	})
}

// A mode switch that restarts the compositor posts ModeChanged on the
// screen whose mode changed. The message has the old and the new mode.
func TestAModeSwitchPostsModeChanged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture, notices := screensOfTwoNodes(t)
		plugin := modeSwitchPlugin(t)
		plugin.notices = notices
		portable := Output{Connector: "HDMI-A-2"}

		if err := plugin.applyMode(t.Context(), portable, "1280x720"); err != nil {
			t.Fatalf("applying the mode: %v", err)
		}
		synctest.Wait()

		want := []string{"Normal ModeChanged Changed the mode of HDMI-A-2 from 1920x1080@60 to 1280x720. " +
			"The compositor restarts, and every screen on the card goes blank"}
		if got := eventLines(fixture.events.About("Display", "screen-b")); !slices.Equal(got, want) {
			t.Errorf("screen-b has the Events %q, want %q", got, want)
		}
	})
}

// modeSwitchPlugin returns a plugin with a fake compositor and no
// layout module, so a mode switch runs inside a synctest bubble.
func modeSwitchPlugin(t *testing.T) *draPlugin {
	t.Helper()
	plugin, _ := modeSwitchBench(t)
	return plugin
}

// modeSwitchBench returns the same plugin and its fake compositor, for
// a test that changes how the compositor restarts.
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
