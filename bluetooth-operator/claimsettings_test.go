package main

import (
	"encoding/json"
	"testing"
)

// airRemote has key and pointer events on one node, so a claim for
// one class gets only part of the node.
func airRemote() evdevCapabilities {
	return evdevCapabilities{
		Name: "Air Remote",
		Codes: map[string][]uint16{
			"EV_KEY": append(codeRange(103, 116), 0x110, 0x111),
			"EV_REL": {0x00, 0x01, 0x08},
			"EV_MSC": {0x04},
		},
	}
}

func (k *fakeKernel) registerNode(path string, caps evdevCapabilities) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.capabilities[path] = caps
}

// relaysOverOneNode returns relays for one connected controller with
// one real node.
func relaysOverOneNode(t *testing.T, caps evdevCapabilities) (*fakeKernel, *relays) {
	t.Helper()
	kernel := newFakeKernel()
	kernel.registerNode("/dev/input/event5", caps)
	set := newRelays(kernel)
	t.Cleanup(func() { set.stop(testMAC) })
	set.ensure(testMAC, []string{"/dev/input/event5"})
	return kernel, set
}

func TestANodeWithNoClaimPassesNothing(t *testing.T) {
	kernel, _ := relaysOverOneNode(t, dualSenseGamepad())

	node := kernel.node(t, "/dev/input/event5")
	for _, event := range []uint16{evKey, evAbs, evRel, evMsc} {
		if node.passes(event) {
			t.Errorf("the node delivers event type %d with no claim prepared", event)
		}
	}
}

// A claim with no inputs is prepared with everyInputClass.
func TestAClaimWithNoInputsReceivesEveryClass(t *testing.T) {
	kernel, set := relaysOverOneNode(t, dualSenseGamepad())

	set.prepare(testMAC, testClaimUID, inputSettings{classes: everyInputClass})

	node := kernel.node(t, "/dev/input/event5")
	for _, event := range []uint16{evKey, evAbs, evRel, evMsc} {
		if !node.passes(event) {
			t.Errorf("the node does not deliver event type %d", event)
		}
	}
}

func TestAClaimMasksANodeToTheClassesItRequests(t *testing.T) {
	kernel, set := relaysOverOneNode(t, airRemote())

	set.prepare(testMAC, testClaimUID, inputSettings{classes: classKey})

	node := kernel.node(t, "/dev/input/event5")
	if !node.passes(evKey) || !node.passes(evMsc) {
		t.Error("the node does not deliver the key events the claim asked for")
	}
	if node.passes(evRel) {
		t.Error("the node still delivers the pointer events no claim asked for")
	}
}

func TestTwoClaimsOnOneControllerPassTheUnionOfTheirClasses(t *testing.T) {
	kernel, set := relaysOverOneNode(t, airRemote())
	const otherClaim = "8b2c4d6e-1f30-4a5b-9c7d-0e1f2a3b4c5d"

	set.prepare(testMAC, testClaimUID, inputSettings{classes: classKey})
	set.prepare(testMAC, otherClaim, inputSettings{classes: classMouse})

	node := kernel.node(t, "/dev/input/event5")
	if !node.passes(evKey) || !node.passes(evRel) {
		t.Error("the node does not deliver both claims' classes")
	}

	set.unprepare(otherClaim)
	if !node.passes(evKey) {
		t.Error("the node stopped delivering the remaining claim's class")
	}
	if node.passes(evRel) {
		t.Error("the node still delivers the class of the claim that ended")
	}
}

// A wider request replaces the mask on the open descriptor, without
// opening the real node again.
func TestAWiderRequestNeedsNoReconnect(t *testing.T) {
	kernel, set := relaysOverOneNode(t, airRemote())
	node := kernel.node(t, "/dev/input/event5")

	set.prepare(testMAC, testClaimUID, inputSettings{classes: classKey})
	if node.passes(evRel) {
		t.Fatal("the node delivers the pointer events before anything asked for them")
	}

	set.prepare(testMAC, testClaimUID, inputSettings{classes: everyInputClass})

	if !node.passes(evRel) {
		t.Error("the widened claim does not receive the pointer events")
	}
	if kernel.opened() != 1 {
		t.Errorf("the relay opened %d real nodes, want one", kernel.opened())
	}
}

func TestUnprepareLeavesANodePassingNothing(t *testing.T) {
	kernel, set := relaysOverOneNode(t, dualSenseGamepad())
	set.prepare(testMAC, testClaimUID, inputSettings{classes: everyInputClass})

	set.unprepare(testClaimUID)

	if kernel.node(t, "/dev/input/event5").passes(evKey) {
		t.Error("the node still delivers events after the last claim ended")
	}
}

// A reconnect opens a new descriptor, which has no mask, so the relay
// sets the mask again when it opens the node.
func TestAReconnectedNodeGetsTheSettingsOfItsClaims(t *testing.T) {
	kernel, set := relaysOverOneNode(t, airRemote())
	set.prepare(testMAC, testClaimUID, inputSettings{classes: classKey})

	// The controller reconnects as a different event node.
	if err := kernel.writer(t, "/dev/input/event5").Close(); err != nil {
		t.Fatalf("closing the first event node: %v", err)
	}
	kernel.registerNode("/dev/input/event9", airRemote())
	waitFor(t, "the relay to read the node that returned", func() bool {
		set.ensure(testMAC, []string{"/dev/input/event9"})
		return kernel.opened() == 2
	})

	node := kernel.node(t, "/dev/input/event9")
	if !node.passes(evKey) {
		t.Error("the reconnected node does not deliver the class the claim asked for")
	}
	if node.passes(evRel) {
		t.Error("the reconnected node delivers a class no claim asked for")
	}
}

func TestRestorePreparedRebuildsTheSettingsFromTheSpecFiles(t *testing.T) {
	cdiTempDir(t)
	first := newRelays(newFakeKernel())
	plugin := &draPlugin{
		client: testClient(t, configuredClaim(t,
			[]AllocatedConfig{driverConfig("FromClaim", `{"inputs":["key"]}`)},
			controllerAllocation())),
		relays: first,
	}
	first.restore(testMAC, storedCapabilities(t))
	if resp := plugin.prepareClaim(testClaim()); resp.Error != "" {
		t.Fatalf("preparing the claim: %s", resp.Error)
	}
	first.stop(testMAC)

	// The replacement pod, with the controller connected.
	kernel, restarted := relaysOverOneNode(t, airRemote())
	restarted.restorePrepared()

	node := kernel.node(t, "/dev/input/event5")
	if !node.passes(evKey) {
		t.Error("the restored demand does not deliver the class the claim asked for")
	}
	if node.passes(evRel) {
		t.Error("the restored demand delivers a class the claim did not ask for")
	}
}

// A DualSense has separate nodes for the gamepad, the accelerometer,
// and the touchpad.
func TestRelayClassesUnionEveryNodeOfAController(t *testing.T) {
	kernel := newFakeKernel()
	kernel.registerNode("/dev/input/event5", dualSenseGamepad())
	kernel.registerNode("/dev/input/event6", dualSenseMotion())
	kernel.registerNode("/dev/input/event7", dualSenseTouchpad())
	set := newRelays(kernel)
	t.Cleanup(func() { set.stop(testMAC) })

	set.ensure(testMAC, []string{"/dev/input/event5", "/dev/input/event6", "/dev/input/event7"})

	want := classJoystick | classAccelerometer | classTouchpad
	if got := set.classes(testMAC); got != want {
		t.Errorf("classes = %s, want %s", got, want)
	}
}

// A disconnected controller still has classes, because the relay
// reads them from the snapshot in the bond's `Secret`.
func TestRelayClassesComeBackFromTheStoredSnapshot(t *testing.T) {
	kernel := newFakeKernel()
	kernel.registerNode("/dev/input/event5", dualSenseGamepad())
	first := newRelays(kernel)
	first.ensure(testMAC, []string{"/dev/input/event5"})
	stored := first.snapshot(testMAC)
	first.stop(testMAC)

	// A new pod, with the controller disconnected and no real node.
	restarted := newRelays(newFakeKernel())
	t.Cleanup(func() { restarted.stop(testMAC) })
	restarted.restore(testMAC, stored)

	if got := restarted.classes(testMAC); got != classJoystick {
		t.Errorf("classes = %s, want joystick", got)
	}
}

func TestRelayClassesAreNoneForABondThatHasNeverConnected(t *testing.T) {
	set := newRelays(newFakeKernel())
	if got := set.classes(testMAC); got != 0 {
		t.Errorf("classes = %s, want none", got)
	}
}

// padWithoutSmoothing is a gamepad whose axes have fuzz 0 and flat 0.
func padWithoutSmoothing() evdevCapabilities {
	return evdevCapabilities{
		Name: "Wireless Controller",
		Codes: map[string][]uint16{
			"EV_KEY": codeRange(0x130, 0x13e),
			"EV_ABS": {0x00, 0x01, 0x03, 0x04},
		},
		Axes: []absAxis{
			{Code: 0x00, Minimum: 0, Maximum: 255},
			{Code: 0x01, Minimum: 0, Maximum: 255},
			{Code: 0x03, Minimum: 0, Maximum: 255},
			{Code: 0x04, Minimum: 0, Maximum: 255},
		},
	}
}

func TestAClaimTunesOnlyTheAxesItSets(t *testing.T) {
	kernel, set := relaysOverOneNode(t, padWithoutSmoothing())

	set.prepare(testMAC, testClaimUID, inputSettings{
		classes: classJoystick,
		axes:    axisOverrides{0x03: {Fuzz: ptr(int32(4))}, 0x04: {Fuzz: ptr(int32(4))}},
	})

	node := kernel.node(t, "/dev/input/event5")
	for _, code := range []uint16{0x03, 0x04} {
		if got := node.axis(code).Fuzz; got != 4 {
			t.Errorf("%s reports fuzz %d, want 4", absCodeName(code), got)
		}
	}
	for _, code := range []uint16{0x00, 0x01} {
		if got := node.axis(code).Fuzz; got != 0 {
			t.Errorf("%s reports fuzz %d, want the device's own 0", absCodeName(code), got)
		}
	}
	if got := node.writes(); got != 2 {
		t.Errorf("the relay wrote %d axes, want the two the claim sets", got)
	}
}

func TestANodeWithNoClaimGetsNoAxisWrite(t *testing.T) {
	kernel, _ := relaysOverOneNode(t, padWithoutSmoothing())

	if got := kernel.node(t, "/dev/input/event5").writes(); got != 0 {
		t.Errorf("the relay wrote %d axes with no claim prepared", got)
	}
}

func TestTwoClaimsSetEachAxisToTheLargestValue(t *testing.T) {
	kernel, set := relaysOverOneNode(t, padWithoutSmoothing())
	const otherClaim = "8b2c4d6e-1f30-4a5b-9c7d-0e1f2a3b4c5d"

	set.prepare(testMAC, testClaimUID, inputSettings{
		classes: classJoystick,
		axes:    axisOverrides{0x03: {Fuzz: ptr(int32(4))}},
	})
	set.prepare(testMAC, otherClaim, inputSettings{
		classes: classJoystick,
		axes:    axisOverrides{0x03: {Fuzz: ptr(int32(8)), Flat: ptr(int32(15))}},
	})

	node := kernel.node(t, "/dev/input/event5")
	if got := node.axis(0x03); got.Fuzz != 8 || got.Flat != 15 {
		t.Errorf("ABS_RX reports fuzz %d and flat %d, want 8 and 15", got.Fuzz, got.Flat)
	}

	// With the larger claim gone, the axis gets the other claim's fuzz
	// and the device's own flat.
	set.unprepare(otherClaim)
	if got := node.axis(0x03); got.Fuzz != 4 || got.Flat != 0 {
		t.Errorf("ABS_RX reports fuzz %d and flat %d, want 4 and the device's own 0", got.Fuzz, got.Flat)
	}
}

func TestUnprepareRestoresTheDevicesOwnAxisValues(t *testing.T) {
	device := padWithoutSmoothing()
	device.Axes[2] = absAxis{Code: 0x03, Minimum: 0, Maximum: 255, Fuzz: 1, Flat: 2}
	kernel, set := relaysOverOneNode(t, device)

	set.prepare(testMAC, testClaimUID, inputSettings{
		classes: classJoystick,
		axes:    axisOverrides{0x03: {Fuzz: ptr(int32(4)), Flat: ptr(int32(15))}},
	})
	set.unprepare(testClaimUID)

	if got := kernel.node(t, "/dev/input/event5").axis(0x03); got.Fuzz != 1 || got.Flat != 2 {
		t.Errorf("ABS_RX reports fuzz %d and flat %d, want the device's own 1 and 2", got.Fuzz, got.Flat)
	}
}

// A reconnect creates a new kernel device without the fuzz the relay
// wrote, so the relay writes it again.
func TestAReconnectedNodeIsTunedAgain(t *testing.T) {
	kernel, set := relaysOverOneNode(t, padWithoutSmoothing())
	set.prepare(testMAC, testClaimUID, inputSettings{
		classes: classJoystick,
		axes:    axisOverrides{0x03: {Fuzz: ptr(int32(4))}},
	})

	if err := kernel.writer(t, "/dev/input/event5").Close(); err != nil {
		t.Fatalf("closing the first event node: %v", err)
	}
	kernel.registerNode("/dev/input/event9", padWithoutSmoothing())
	waitFor(t, "the relay to read the node that returned", func() bool {
		set.ensure(testMAC, []string{"/dev/input/event9"})
		return kernel.opened() == 2
	})

	if got := kernel.node(t, "/dev/input/event9").axis(0x03).Fuzz; got != 4 {
		t.Errorf("the reconnected node reports fuzz %d, want 4", got)
	}
}

// After a restart with the controller still connected, the kernel
// reports the fuzz that the previous pod wrote. The snapshot must keep
// the device's own fuzz, or no later unprepare could restore it.
func TestTheSnapshotKeepsTheDevicesOwnValuesUnderATunedAxis(t *testing.T) {
	device := padWithoutSmoothing()
	device.Axes[2] = absAxis{Code: 0x03, Minimum: 0, Maximum: 255, Fuzz: 1}
	kernel, set := relaysOverOneNode(t, device)
	set.prepare(testMAC, testClaimUID, inputSettings{
		classes: classJoystick,
		axes:    axisOverrides{0x03: {Fuzz: ptr(int32(4))}},
	})

	tuned := device
	tuned.Axes = append([]absAxis(nil), device.Axes...)
	tuned.Axes[2] = absAxis{Code: 0x03, Minimum: 0, Maximum: 255, Fuzz: 4}
	kernel.registerNode("/dev/input/event5", tuned)
	set.ensure(testMAC, []string{"/dev/input/event5"})

	var stored evdevSnapshot
	if err := json.Unmarshal(set.snapshot(testMAC), &stored); err != nil {
		t.Fatalf("decoding the snapshot of the relays: %v", err)
	}
	for _, axis := range stored.Nodes[0].Axes {
		if axis.Code == 0x03 && axis.Fuzz != 1 {
			t.Errorf("the snapshot records fuzz %d for ABS_RX, want the device's own 1", axis.Fuzz)
		}
	}
}

func TestRestorePreparedRebuildsTheAxesFromTheSpecFiles(t *testing.T) {
	cdiTempDir(t)
	first := newRelays(newFakeKernel())
	plugin := &draPlugin{
		client: testClient(t, configuredClaim(t,
			[]AllocatedConfig{driverConfig("FromClaim",
				`{"inputs":["joystick"],"axes":{"ABS_RX":{"fuzz":4}}}`)},
			controllerAllocation())),
		relays: first,
	}
	first.restore(testMAC, storedCapabilities(t))
	if resp := plugin.prepareClaim(testClaim()); resp.Error != "" {
		t.Fatalf("preparing the claim: %s", resp.Error)
	}
	first.stop(testMAC)

	kernel, restarted := relaysOverOneNode(t, padWithoutSmoothing())
	restarted.restorePrepared()

	if got := kernel.node(t, "/dev/input/event5").axis(0x03).Fuzz; got != 4 {
		t.Errorf("the restored demand reports fuzz %d for ABS_RX, want 4", got)
	}
}
