package main

// These tests cover the panel desire a screen client left on the bus
// before its Player turned on. The client lowered its shade and stated
// off when the Player turned off, and the retained off stays on the
// topic until the client answers the wake with on. A pass that runs in
// between must not darken the panel it just lit.

import (
	"strings"
	"testing"
	"time"
)

// clockedOperator is an operator over the cluster whose pass and panel
// desk read one clock the test moves.
func clockedOperator(t *testing.T, cluster *fakeCluster) (*operator, *testClock) {
	t.Helper()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	clock := &testClock{now: gapStart}
	media.now = clock.read
	media.panels.now = clock.read
	return media, clock
}

// turnedOn runs a theater that is off, with the client's off desire on
// the bus, through the pass that turns it on, and moves the clock past
// that pass.
func turnedOn(t *testing.T, cluster *fakeCluster) (*operator, *testClock) {
	t.Helper()
	cluster.players["theater"].Spec.Power = playerPowerOff
	cluster.players["theater"].Status.Power = playerPowerOff
	media, clock := clockedOperator(t, cluster)
	statePanelDesire(media, panelDesireOff)
	powerPass(media, cluster, nil)
	clock.now = clock.now.Add(time.Minute)
	cluster.players["theater"].Spec.Power = playerPowerOn
	powerPass(media, cluster, nil)
	clock.now = clock.now.Add(time.Second)
	return media, clock
}

// appliedOverrides names each override the passes applied, in order.
func appliedOverrides(cluster *fakeCluster) string {
	names := make([]string, len(cluster.applies))
	for index, applied := range cluster.applies {
		names[index] = describeOverride(applied.override)
	}
	return strings.Join(names, ", ")
}

// An off desire that arrived before the turn-on is the client's word
// from before the Player turned off, and the pass waits for the next
// one. An off desire that arrives after it is the client's own off
// window, and it darkens the panel.
func TestAnOffDesireFromBeforeTheTurnOnWaitsForTheNext(t *testing.T) {
	cases := []struct {
		name  string
		fresh bool
		want  string
	}{
		{name: "the off desire left from before", want: "power Off, no override"},
		{name: "an off desire after the turn-on", fresh: true, want: "power Off, no override, backlight Off"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := screenCluster()
			media, _ := turnedOn(t, cluster)
			if each.fresh {
				statePanelDesire(media, panelDesireOn)
				statePanelDesire(media, panelDesireOff)
			}

			powerPass(media, cluster, nil)

			mustMatch(t, appliedOverrides(cluster), each.want)
		})
	}
}

// The session's awake flag waits the same way: it stays as the turn-on
// left it until the client states on, and then the equipment wakes.
func TestTheAwakeFlagWaitsForTheClientAfterATurnOn(t *testing.T) {
	cluster := receiverCluster()
	media, _ := turnedOn(t, cluster)

	powerPass(media, cluster, nil)
	statePanelDesire(media, panelDesireOn)
	powerPass(media, cluster, nil)

	mustMatch(t, appliedAwake(cluster), "false, true")
	mustMatch(t, strings.Contains(appliedOverrides(cluster), "backlight"), false)
}

// The status records when the Player last turned on, so a restarted
// operator reads the same moment.
func TestTheTurnOnTimeIsRecordedInTheStatus(t *testing.T) {
	cluster := screenCluster()
	media, clock := turnedOn(t, cluster)

	mustMatch(t, cluster.players["theater"].Status.PoweredOnAt,
		clock.now.Add(-time.Second).UTC().Format(askTimeLayout))
	powerPass(media, cluster, nil)
	mustMatch(t, cluster.players["theater"].Status.PoweredOnAt,
		clock.now.Add(-time.Second).UTC().Format(askTimeLayout))
}
