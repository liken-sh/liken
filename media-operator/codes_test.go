package main

// These tests cover the codes desk, the boundary between the standing
// pod's declared-codes document and the reconcile pass, and the gap
// the pass reports.

import "testing"

func testDeclared() remoteCodes {
	return remoteCodes{Keys: []uint16{0x130, 0x131}, Axes: []uint16{0x10}}
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

// The status reports the gap, which is what the controller declares
// minus what the Keymap binds, with each entry carrying the code, its
// evdev name, and its event type.
func TestTheGapIsWhatTheKeymapDoesNotBind(t *testing.T) {
	cases := []struct {
		name     string
		declared remoteCodes
		bindings []compiledBinding
		want     []UnboundCode
	}{
		{
			name:     "a controller with no Keymap at all",
			declared: remoteCodes{Keys: []uint16{0x130}, Axes: []uint16{0x11}},
			want: []UnboundCode{
				{Code: 0x130, Name: "BTN_SOUTH", Type: unboundKey},
				{Code: 0x11, Name: "ABS_HAT0Y", Type: unboundAxis},
			},
		},
		{
			name:     "a Keymap that binds every code",
			declared: remoteCodes{Keys: []uint16{0x130}, Axes: []uint16{0x11}},
			bindings: []compiledBinding{
				{EventType: evKey, Code: 0x130, Value: 1, Action: actionPause},
				{EventType: evAbs, Code: 0x11, Value: -1, Action: actionUp},
			},
			want: nil,
		},
		{
			name:     "one direction of a hat binds the axis",
			declared: remoteCodes{Axes: []uint16{0x10, 0x11}},
			bindings: []compiledBinding{
				{EventType: evAbs, Code: 0x11, Value: 1, Action: actionDown},
			},
			want: []UnboundCode{{Code: 0x10, Name: "ABS_HAT0X", Type: unboundAxis}},
		},
		{
			name:     "a binding on a code the controller does not declare",
			declared: remoteCodes{Keys: []uint16{0x130}},
			bindings: []compiledBinding{
				{EventType: evKey, Code: 0x131, Value: 1, Action: actionPause},
			},
			want: []UnboundCode{{Code: 0x130, Name: "BTN_SOUTH", Type: unboundKey}},
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
			mustMatchAll(t, unboundCodes(each.declared, each.bindings), each.want)
		})
	}
}
