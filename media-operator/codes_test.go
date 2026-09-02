package main

// These tests cover the codes desk, the boundary between the standing
// pod's declared-codes document and the reconcile pass, and the gap
// the pass reports.

import (
	"testing"
	"time"
)

func testDeclared() remoteCodes {
	return remoteCodes{Keys: []uint16{0x130, 0x131}, Axes: []uint16{0x10}}
}

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

// A fresh document is a status the pass must rewrite, so it wakes the
// loop.
func TestADeclaredCodeSetWakesTheLoop(t *testing.T) {
	wake := make(chan struct{}, 1)
	desk := newCodesDesk(wake)

	desk.setCodes(controllerKey("house", "sofa"), testDeclared())

	mustMatch(t, woke(wake), true)
}

// The retained topic delivers the same document on every reconnect, so
// a repeat wakes nothing.
func TestARepeatedCodeSetWakesNothing(t *testing.T) {
	wake := make(chan struct{}, 1)
	desk := newCodesDesk(wake)
	desk.setCodes(controllerKey("house", "sofa"), testDeclared())
	<-wake

	desk.setCodes(controllerKey("house", "sofa"), testDeclared())

	mustMatch(t, woke(wake), false)
}

// A controller whose nodes vanished clears its topic, and the desk
// then holds nothing for it.
func TestAClearedDocumentLeavesTheDeskHoldingNothing(t *testing.T) {
	wake := make(chan struct{}, 1)
	desk := newCodesDesk(wake)
	desk.setCodes(controllerKey("house", "sofa"), testDeclared())
	<-wake

	desk.clear(controllerKey("house", "sofa"))

	_, held := desk.codesFor(controllerKey("house", "sofa"))
	mustMatch(t, held, false)
	mustMatch(t, woke(wake), true)
}

// A retained document outlives the pod that wrote it, so a document
// stops standing while its pod is offline.
func TestAnOfflinePodHoldsNoDeclaredCodes(t *testing.T) {
	desk := newCodesDesk(nil)
	desk.setCodes(controllerKey("house", "sofa"), testDeclared())

	desk.setAvailability(controllerKey("house", "sofa"), false)

	_, held := desk.codesFor(controllerKey("house", "sofa"))
	mustMatch(t, held, false)

	desk.setAvailability(controllerKey("house", "sofa"), true)
	codes, held := desk.codesFor(controllerKey("house", "sofa"))
	mustMatch(t, held, true)
	mustMatchAll(t, codes.Keys, testDeclared().Keys)
}

// The desk shrinks to the Remotes the cluster still holds.
func TestTheCodesDeskDropsAControllerTheClusterNoLongerHolds(t *testing.T) {
	desk := newCodesDesk(nil)
	desk.setCodes(controllerKey("house", "sofa"), testDeclared())
	desk.setAvailability(controllerKey("house", "sofa"), true)
	desk.setCodes(controllerKey("house", "gone"), testDeclared())

	desk.retain(map[string]bool{controllerKey("house", "sofa"): true})

	_, held := desk.codesFor(controllerKey("house", "sofa"))
	mustMatch(t, held, true)
	_, stillHeld := desk.codesFor(controllerKey("house", "gone"))
	mustMatch(t, stillHeld, false)
}

// The status reports the gap: every declared control the table maps to
// nothing, each entry with the code, its evdev name, and its event
// type.
func TestTheGapIsWhatTheTableMapsToNothing(t *testing.T) {
	cases := []struct {
		name     string
		declared remoteCodes
		table    []compiledBinding
		want     []UnboundCode
	}{
		{
			name:     "a controller on the base alone",
			declared: remoteCodes{Keys: []uint16{0x130}, Axes: []uint16{0x11}},
			table:    baseKeys,
			want:     nil,
		},
		{
			name:     "a key the table drops with none",
			declared: remoteCodes{Keys: []uint16{0x130, 0x131}},
			table: []compiledBinding{
				{EventType: evKey, Code: 0x131, Value: 1, Key: keyNone},
			},
			want: []UnboundCode{{Code: 0x131, Name: "BTN_EAST", Type: unboundKey}},
		},
		{
			name:     "a key with no row at all passes as itself",
			declared: remoteCodes{Keys: []uint16{0x0a4}},
			want:     nil,
		},
		{
			name:     "one direction of a hat binds the axis",
			declared: remoteCodes{Axes: []uint16{0x10, 0x11}},
			table: []compiledBinding{
				{EventType: evAbs, Code: 0x11, Value: 1, Key: "KEY_DOWN"},
			},
			want: []UnboundCode{{Code: 0x10, Name: "ABS_HAT0X", Type: unboundAxis}},
		},
		{
			name:     "a hat both of whose directions the table drops",
			declared: remoteCodes{Axes: []uint16{0x11}},
			table: []compiledBinding{
				{EventType: evAbs, Code: 0x11, Value: -1, Key: keyNone},
				{EventType: evAbs, Code: 0x11, Value: 1, Key: keyNone},
			},
			want: []UnboundCode{{Code: 0x11, Name: "ABS_HAT0Y", Type: unboundAxis}},
		},
		{
			name:     "a code the kernel names nothing",
			declared: remoteCodes{Keys: []uint16{0x2ff}},
			want:     []UnboundCode{{Code: 0x2ff, Type: unboundKey}},
		},
		{
			name:     "a controller that declares nothing",
			declared: remoteCodes{},
			want:     nil,
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatchAll(t, unboundCodes(each.declared, each.table), each.want)
		})
	}
}
