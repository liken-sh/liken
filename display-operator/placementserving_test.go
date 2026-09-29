package main

// The CompositorServing condition on a Display whose monitor is not on
// the wire. No screen of the pass reports such a Display, so these
// drills prove that the condition still follows the compositor.

import "testing"

// stoppedCompositor is the probe's answer while the socket refuses the
// connect.
func stoppedCompositor() compositorLiveness {
	return compositorLiveness{reason: CompositorDownReason, detail: "connect: no such file or directory"}
}

// servingCondition is the compositor condition one Display reports.
func servingCondition(f *placementFixture, monitor EDID) DisplayCondition {
	f.t.Helper()
	return conditionByType(f.status(monitor), CompositorServingCondition)
}

// A monitor that left keeps its Display on this node, and the Display
// reports the compositor that stopped. The compositor that comes back
// serves this node's screens, and the Display says so, though no
// screen reports it.
func TestADisplayWhoseMonitorLeftFollowsTheCompositorBack(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	fixture.run()
	fixture.darken("HDMI-A-1")
	fixture.probes(stoppedCompositor())
	fixture.run()
	if condition := servingCondition(fixture, labMonitor()); condition.Status != conditionFalse {
		t.Fatalf("%s is %s under a compositor that serves nobody", CompositorServingCondition, condition.Status)
	}

	fixture.serves()
	fixture.run()

	condition := servingCondition(fixture, labMonitor())
	if condition.Status != conditionTrue || condition.Reason != CompositorServingReason {
		t.Errorf("%s is %s/%s under a compositor that serves again, want %s/%s", CompositorServingCondition,
			condition.Status, condition.Reason, conditionTrue, CompositorServingReason)
	}
}

// An operator that starts does not know what an earlier operator wrote
// while the compositor was down. A Display on this node whose monitor
// is not on the wire takes the answer of the compositor that serves
// now.
func TestAnOperatorThatStartsCorrectsADisplayWhoseMonitorLeft(t *testing.T) {
	fixture := newPlacementFixture(t, deskScreen())
	left := fixture.screen(labMonitor(), DisplaySpec{})
	left.Status.Conditions = setCondition(nil, fixture.pass.serving(stoppedCompositor()))

	fixture.run()

	if condition := servingCondition(fixture, labMonitor()); condition.Status != conditionTrue {
		t.Errorf("%s is %s/%s under a compositor that serves, want %s", CompositorServingCondition,
			condition.Status, condition.Reason, conditionTrue)
	}
}

// Another node's compositor is not this node's to report.
func TestADisplayOnAnotherNodeKeepsItsCondition(t *testing.T) {
	fixture := newPlacementFixture(t, deskScreen())
	elsewhere := fixture.screen(labMonitor(), DisplaySpec{})
	elsewhere.Status.Node = "node-2"
	elsewhere.Status.Conditions = setCondition(nil, fixture.pass.serving(stoppedCompositor()))

	fixture.run()

	if condition := servingCondition(fixture, labMonitor()); condition.Status != conditionFalse {
		t.Errorf("the pass wrote %s/%s on another node's Display", condition.Status, condition.Reason)
	}
}

// The Displays are listed once when the operator starts and once after
// each time the compositor stopped. A list that failed is made again on
// the next pass, and a pass in the steady state lists nothing.
func TestTheDisplaysAreListedOnlyUntilEachCarriesTheAnswer(t *testing.T) {
	fixture := newPlacementFixture(t, deskScreen())
	left := fixture.screen(labMonitor(), DisplaySpec{})
	left.Status.Conditions = setCondition(nil, fixture.pass.serving(stoppedCompositor()))
	fixture.refuse[DisplaysPath] = 1

	if err := fixture.pass.pass(); err == nil {
		t.Fatal("a pass whose list of the Displays failed reports no failure")
	}
	fixture.run()
	if condition := servingCondition(fixture, labMonitor()); condition.Status != conditionTrue {
		t.Fatalf("%s is %s after the pass that listed the Displays again", CompositorServingCondition, condition.Status)
	}

	lists := fixture.displayLists
	fixture.run()
	if fixture.displayLists != lists {
		t.Errorf("a steady pass listed the Displays %d more times, want none", fixture.displayLists-lists)
	}
}

// A monitor that leaves while the compositor serves takes its windows
// off the screen with it: the compositor destroys the output, and the
// module hides each window that was on it. The Display then reports no
// windows and no arrangement, the same as a Display whose compositor
// stopped.
func TestADisplayWhoseMonitorLeftReportsNoSurfaces(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.hold("living-room", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("living-room", "film-0", nil)
	fixture.surface(film, 1920, 1080)
	fixture.run()
	if status := fixture.status(labMonitor()); len(status.Surfaces) != 1 || status.Layout == nil {
		t.Fatalf("the screen reports %+v and %+v while its monitor is on the wire", status.Surfaces, status.Layout)
	}

	fixture.darken("HDMI-A-1")
	fixture.run()

	status := fixture.status(labMonitor())
	if status.Surfaces != nil {
		t.Errorf("the screen reports %+v with no monitor on the wire", status.Surfaces)
	}
	if status.Layout != nil {
		t.Errorf("the screen reports the layout %+v with no monitor on the wire", status.Layout)
	}
	if condition := servingCondition(fixture, labMonitor()); condition.Status != conditionTrue {
		t.Errorf("%s is %s under a compositor that serves", CompositorServingCondition, condition.Status)
	}
}

// The monitor that comes back is reported again from the pass that
// finds it, and the pass after that lists nothing.
func TestAMonitorThatReturnsIsReportedAgain(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.hold("living-room", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("living-room", "film-0", nil)
	fixture.surface(film, 1920, 1080)
	fixture.run()
	fixture.darken("HDMI-A-1")
	fixture.run()

	fixture.light(livingRoomScreen())
	fixture.run()

	if status := fixture.status(labMonitor()); len(status.Surfaces) != 1 || status.Layout == nil {
		t.Errorf("the screen reports %+v and %+v after its monitor returned", status.Surfaces, status.Layout)
	}
	lists := fixture.displayLists
	fixture.run()
	if fixture.displayLists != lists {
		t.Errorf("a steady pass listed the Displays %d more times, want none", fixture.displayLists-lists)
	}
}
