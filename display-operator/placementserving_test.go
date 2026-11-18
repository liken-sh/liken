package main

import "testing"

func stoppedCompositor() compositorLiveness {
	return compositorLiveness{reason: CompositorDownReason, detail: "connect: no such file or directory"}
}

func servingCondition(f *placementFixture, monitor EDID) DisplayCondition {
	f.t.Helper()
	return conditionByType(f.status(monitor), CompositorServingCondition)
}

// No screen reports the Display of a disconnected monitor, so these
// tests check that sweep and reportNoCompositor keep its
// CompositorServing condition in step with the compositor.
func TestADisplayWhoseMonitorIsDisconnectedFollowsTheCompositor(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	fixture.run()
	fixture.disconnect("HDMI-A-1")
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

// A new operator process cannot tell what the previous one wrote while
// the compositor was down, so its first pass with a serving compositor
// sweeps. The sweep corrects a stale condition on a Display whose
// monitor is disconnected.
func TestAnOperatorThatStartsCorrectsADisplayWhoseMonitorIsDisconnected(t *testing.T) {
	fixture := newPlacementFixture(t, secondaryScreen())
	left := fixture.screen(labMonitor(), DisplaySpec{})
	left.Status.Conditions = setCondition(nil, fixture.pass.serving(stoppedCompositor()))

	fixture.run()

	if condition := servingCondition(fixture, labMonitor()); condition.Status != conditionTrue {
		t.Errorf("%s is %s/%s under a compositor that serves, want %s", CompositorServingCondition,
			condition.Status, condition.Reason, conditionTrue)
	}
}

// This node does not write the condition of a Display that another
// node owns.
func TestADisplayOnAnotherNodeKeepsItsCondition(t *testing.T) {
	fixture := newPlacementFixture(t, secondaryScreen())
	elsewhere := fixture.screen(labMonitor(), DisplaySpec{})
	elsewhere.Status.Node = "node-2"
	elsewhere.Status.Conditions = setCondition(nil, fixture.pass.serving(stoppedCompositor()))

	fixture.run()

	if condition := servingCondition(fixture, labMonitor()); condition.Status != conditionFalse {
		t.Errorf("the pass wrote %s/%s on another node's Display", condition.Status, condition.Reason)
	}
}

// While the compositor serves, the pass lists the Displays only while
// sweepPending is set: when the operator starts, after the compositor
// stops, and when a monitor connects or disconnects. A failed list
// leaves sweepPending set, so the next pass lists again.
func TestTheDisplaysAreListedOnlyWhileASweepIsPending(t *testing.T) {
	fixture := newPlacementFixture(t, secondaryScreen())
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

func TestADisplayWhoseMonitorIsDisconnectedReportsNoSurfaces(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("media", "film-0", nil)
	fixture.surface(film, 1920, 1080)
	fixture.run()
	if status := fixture.status(labMonitor()); len(status.Surfaces) != 1 || status.Layout == nil {
		t.Fatalf("the screen reports %+v and %+v while its monitor is connected", status.Surfaces, status.Layout)
	}

	fixture.disconnect("HDMI-A-1")
	fixture.run()

	status := fixture.status(labMonitor())
	if status.Surfaces != nil {
		t.Errorf("the screen reports %+v with no monitor connected", status.Surfaces)
	}
	if status.Layout != nil {
		t.Errorf("the screen reports the layout %+v with no monitor connected", status.Layout)
	}
	if condition := servingCondition(fixture, labMonitor()); condition.Status != conditionTrue {
		t.Errorf("%s is %s under a compositor that serves", CompositorServingCondition, condition.Status)
	}
}

func TestAMonitorThatReconnectsIsReportedAgain(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	film := fixture.prepareClaim("media", "film", filmClaimUID, "HDMI-A-1", "film-0")
	fixture.pod("media", "film-0", nil)
	fixture.surface(film, 1920, 1080)
	fixture.run()
	fixture.disconnect("HDMI-A-1")
	fixture.run()

	fixture.reconnect(primaryScreen())
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

// The set of connected monitors does not change between the stop and
// the restart, so only reportNoCompositor sets sweepPending.
func TestACompositorThatReturnsAfterTheMonitorDisconnectedIsReported(t *testing.T) {
	fixture := newPlacementFixture(t)
	fixture.screen(labMonitor(), DisplaySpec{})
	fixture.run()
	fixture.disconnect("HDMI-A-1")
	fixture.run()
	fixture.probes(stoppedCompositor())
	fixture.run()

	fixture.serves()
	fixture.run()

	if condition := servingCondition(fixture, labMonitor()); condition.Status != conditionTrue {
		t.Errorf("%s is %s/%s under a compositor that serves again, want %s", CompositorServingCondition,
			condition.Status, condition.Reason, conditionTrue)
	}
}
