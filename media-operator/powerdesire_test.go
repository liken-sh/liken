package main

// When a Player turns off, its screen client publishes a retained off
// panel desire. The broker keeps that message after the Player turns on
// again, until the client publishes on. A pass that runs in between must
// not turn off the panel that the turn-on just turned on.

import (
	"strings"
	"testing"
	"time"
)

// clockedOperator returns an operator whose passes and panel desires
// read a clock that the test advances.
func clockedOperator(t *testing.T, cluster *fakeCluster) (*operator, *testClock) {
	t.Helper()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	clock := &testClock{now: gapStart}
	media.now = clock.read
	media.panels.now = clock.read
	return media, clock
}

// turnedOn runs the passes that turn on a theater that is off, with the
// client's off desire published. It then advances the clock one second,
// so a desire published later is newer than the turn-on.
func turnedOn(t *testing.T, cluster *fakeCluster) (*operator, *testClock) {
	t.Helper()
	cluster.players["theater"].Spec.Power = playerPowerOff
	cluster.players["theater"].Status.Power = playerPowerOff
	media, clock := clockedOperator(t, cluster)
	sendPanelDesire(media, panelDesireOff)
	powerPass(media, cluster, nil)
	clock.now = clock.now.Add(time.Minute)
	cluster.players["theater"].Spec.Power = playerPowerOn
	powerPass(media, cluster, nil)
	clock.now = clock.now.Add(time.Second)
	return media, clock
}

// appliedOverrides lists the overrides that the passes applied, in
// order.
func appliedOverrides(cluster *fakeCluster) string {
	names := make([]string, len(cluster.applies))
	for index, applied := range cluster.applies {
		names[index] = describeOverride(applied.override)
	}
	return strings.Join(names, ", ")
}

// The pass ignores an off desire older than the turn-on, and turns the
// panel off for one that is newer.
func TestAnOffDesireFromBeforeTheTurnOnIsIgnoredUntilTheClientPublishesAgain(t *testing.T) {
	cases := []struct {
		name  string
		fresh bool
		want  string
	}{
		{name: "the off desire retained from before the turn-on", want: "power Off, no override"},
		{name: "an off desire after the turn-on", fresh: true, want: "power Off, no override, backlight Off"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := screenCluster()
			media, _ := turnedOn(t, cluster)
			if each.fresh {
				sendPanelDesire(media, panelDesireOn)
				sendPanelDesire(media, panelDesireOff)
			}

			powerPass(media, cluster, nil)

			mustMatch(t, appliedOverrides(cluster), each.want)
		})
	}
}

// The Receiver session's awake field also ignores the old off desire.
// It stays false after the turn-on until the client publishes on.
func TestTheAwakeFieldWaitsForTheClientAfterATurnOn(t *testing.T) {
	cluster := receiverCluster()
	media, _ := turnedOn(t, cluster)

	powerPass(media, cluster, nil)
	sendPanelDesire(media, panelDesireOn)
	powerPass(media, cluster, nil)

	mustMatch(t, appliedAwake(cluster), "false, true")
	mustMatch(t, strings.Contains(appliedOverrides(cluster), "backlight"), false)
}

// The turn-on time is in the Player's status, so a restarted operator
// reads the same time.
func TestTheTurnOnTimeIsRecordedInTheStatus(t *testing.T) {
	cluster := screenCluster()
	media, clock := turnedOn(t, cluster)

	mustMatch(t, cluster.players["theater"].Status.PoweredOnAt,
		clock.now.Add(-time.Second).UTC().Format(askTimeLayout))
	powerPass(media, cluster, nil)
	mustMatch(t, cluster.players["theater"].Status.PoweredOnAt,
		clock.now.Add(-time.Second).UTC().Format(askTimeLayout))
}
