package main

// These tests cover the presence desk: the wake a change earns, the
// quiet a repeat earns, and the shrink to the Remotes the cluster still
// holds.

import (
	"testing"
	"time"
)

// woke reports whether the desk poked the loop, under a short deadline so
// a desk that wakes nothing fails fast.
func woke(wake <-chan struct{}) bool {
	select {
	case <-wake:
		return true
	case <-time.After(50 * time.Millisecond):
		return false
	}
}

// A controller that connects or disconnects is what a person sees on the
// idle screen, so each change wakes the loop that republishes the status.
func TestAPresenceChangeWakesTheLoop(t *testing.T) {
	wake := make(chan struct{}, 1)
	desk := newPresenceDesk(wake)

	desk.setConnected(controllerKey("house", "sofa"), true)

	mustMatch(t, woke(wake), true)
}

// The retained topic delivers the same message again on every reconnect,
// so a repeat of the value already held wakes nothing.
func TestARepeatedPresenceWakesNothing(t *testing.T) {
	wake := make(chan struct{}, 1)
	desk := newPresenceDesk(wake)
	desk.setConnected(controllerKey("house", "sofa"), true)
	<-wake

	desk.setConnected(controllerKey("house", "sofa"), true)

	mustMatch(t, woke(wake), false)
}

// A pod that goes offline changes the fold, so it wakes the loop the way a
// presence change does.
func TestAnAvailabilityChangeWakesTheLoop(t *testing.T) {
	wake := make(chan struct{}, 1)
	desk := newPresenceDesk(wake)

	desk.setAvailability(controllerKey("house", "sofa"), false)

	mustMatch(t, woke(wake), true)
}

// The desk shrinks to the Remotes the cluster still holds, so a deleted
// Remote leaves no key behind. A controller that is still there keeps the
// presence the desk folded.
func TestTheDeskDropsAControllerTheClusterNoLongerHolds(t *testing.T) {
	desk := newPresenceDesk(nil)
	desk.setConnected(controllerKey("house", "sofa"), true)
	desk.setAvailability(controllerKey("house", "sofa"), true)
	desk.setConnected(controllerKey("house", "gone"), true)

	desk.retain(map[string]bool{controllerKey("house", "sofa"): true})

	connected, held := desk.presenceFor(controllerKey("house", "sofa"))
	mustMatch(t, held, true)
	mustMatch(t, connected, true)
	_, stillHeld := desk.presenceFor(controllerKey("house", "gone"))
	mustMatch(t, stillHeld, false)
}
