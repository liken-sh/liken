package main

// These tests prove that a controller's real nodes deliver only what
// its prepared claims asked for, that a change of demand reaches the
// kernel with no reconnect, and that a restart reads the demand back
// from the files that record what each consumer holds.

import (
	"testing"
)

// airMouse is one node that both types and points, so a claim on one
// of its classes is a claim on part of it.
func airMouse() evdevCapabilities {
	return evdevCapabilities{
		Name: "Air Remote",
		Codes: map[string][]uint16{
			"EV_KEY": append(codeRange(103, 116), 0x110, 0x111),
			"EV_REL": {0x00, 0x01, 0x08},
			"EV_MSC": {0x04},
		},
	}
}

// registerNode declares a real node with the capabilities a test
// chose, rather than the one button register declares.
func (k *fakeKernel) registerNode(path string, caps evdevCapabilities) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.capabilities[path] = caps
}

// relayingOne is a relay desk over a fake kernel with one controller
// on the air, whose single node has these capabilities.
func relayingOne(t *testing.T, caps evdevCapabilities) (*fakeKernel, *relays) {
	t.Helper()
	kernel := newFakeKernel()
	kernel.registerNode("/dev/input/event5", caps)
	held := newRelays(kernel)
	t.Cleanup(func() { held.stop(testMAC) })
	held.ensure(testMAC, []string{"/dev/input/event5"})
	return kernel, held
}

// A controller nothing has claimed delivers nothing. The virtual
// device is still there, so a claim can be prepared against it at any
// moment, and the events cost this operator nothing until one is.
func TestANodeNoClaimHoldsDeliversNothing(t *testing.T) {
	kernel, _ := relayingOne(t, dualSenseGamepad())

	node := kernel.node(t, "/dev/input/event5")
	for _, event := range []uint16{evKey, evAbs, evRel, evMsc} {
		if node.delivers(event) {
			t.Errorf("the node delivers event type %d with no claim prepared", event)
		}
	}
}

// A claim that names no classes receives every one, which is what the
// driver delivered before the parameter existed.
func TestAClaimWithNoInputsReceivesEveryClass(t *testing.T) {
	kernel, held := relayingOne(t, dualSenseGamepad())

	held.prepare(testMAC, testClaimUID, everyInputClass)

	node := kernel.node(t, "/dev/input/event5")
	for _, event := range []uint16{evKey, evAbs, evRel, evMsc} {
		if !node.delivers(event) {
			t.Errorf("the node does not deliver event type %d", event)
		}
	}
}

// The claim that narrows a node is the whole point: a consumer that
// reads keys receives the key events and not the pointer's.
func TestAClaimNarrowsANodeToTheClassesItAsksFor(t *testing.T) {
	kernel, held := relayingOne(t, airMouse())

	held.prepare(testMAC, testClaimUID, classKey)

	node := kernel.node(t, "/dev/input/event5")
	if !node.delivers(evKey) || !node.delivers(evMsc) {
		t.Error("the node does not deliver the key events the claim asked for")
	}
	if node.delivers(evRel) {
		t.Error("the node still delivers the pointer events no claim asked for")
	}
}

// Demand is the union over the prepared claims, so two consumers of
// one controller each receive what they asked for.
func TestTwoClaimsOnOneControllerUnionTheirDemand(t *testing.T) {
	kernel, held := relayingOne(t, airMouse())
	const otherClaim = "8b2c4d6e-1f30-4a5b-9c7d-0e1f2a3b4c5d"

	held.prepare(testMAC, testClaimUID, classKey)
	held.prepare(testMAC, otherClaim, classMouse)

	node := kernel.node(t, "/dev/input/event5")
	if !node.delivers(evKey) || !node.delivers(evRel) {
		t.Error("the node does not deliver both claims' classes")
	}

	// The claim on the pointer ends. What it asked for goes with it,
	// and the claim on the keys keeps what it asked for.
	held.unprepare(otherClaim)
	if !node.delivers(evKey) {
		t.Error("the node stopped delivering the remaining claim's class")
	}
	if node.delivers(evRel) {
		t.Error("the node still delivers the class of the claim that ended")
	}
}

// A demand that widens reaches the kernel on the fd that is already
// open. The controller does not have to reconnect, and the consumer
// that holds the virtual node keeps holding it.
func TestAWidenedDemandNeedsNoReconnect(t *testing.T) {
	kernel, held := relayingOne(t, airMouse())
	node := kernel.node(t, "/dev/input/event5")

	held.prepare(testMAC, testClaimUID, classKey)
	if node.delivers(evRel) {
		t.Fatal("the node delivers the pointer events before anything asked for them")
	}

	held.prepare(testMAC, testClaimUID, everyInputClass)

	if !node.delivers(evRel) {
		t.Error("the widened claim does not receive the pointer events")
	}
	if kernel.opened() != 1 {
		t.Errorf("the relay opened %d real nodes, want one", kernel.opened())
	}
}

// The last claim on a controller ends, and the node stops being read.
func TestUnprepareLeavesANodeDeliveringNothing(t *testing.T) {
	kernel, held := relayingOne(t, dualSenseGamepad())
	held.prepare(testMAC, testClaimUID, everyInputClass)

	held.unprepare(testClaimUID)

	if kernel.node(t, "/dev/input/event5").delivers(evKey) {
		t.Error("the node still delivers events after the last claim ended")
	}
}

// A controller that returns from sleep is read under the demand its
// claims already hold. A fresh fd carries no mask, so the relay sets
// one before the pump reads anything.
func TestAReconnectedNodeTakesTheDemandTheClaimsAlreadyHold(t *testing.T) {
	kernel, held := relayingOne(t, airMouse())
	held.prepare(testMAC, testClaimUID, classKey)

	// The controller slept, and returned on a different event number.
	if err := kernel.writer(t, "/dev/input/event5").Close(); err != nil {
		t.Fatal(err)
	}
	kernel.registerNode("/dev/input/event9", airMouse())
	waitFor(t, "the relay to read the node that returned", func() bool {
		held.ensure(testMAC, []string{"/dev/input/event9"})
		return kernel.opened() == 2
	})

	node := kernel.node(t, "/dev/input/event9")
	if !node.delivers(evKey) {
		t.Error("the reconnected node does not deliver the class the claim asked for")
	}
	if node.delivers(evRel) {
		t.Error("the reconnected node delivers a class no claim asked for")
	}
}

// This operator restarts while a consumer holds a claim. The CDI spec
// file the previous pod wrote records which classes that claim asked
// for, so the replacement narrows the same node the same way.
func TestRestorePreparedRebuildsDemandFromTheSpecFiles(t *testing.T) {
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
		t.Fatalf("prepare failed: %s", resp.Error)
	}
	first.stop(testMAC)

	// The replacement pod, with the controller on the air.
	kernel, restarted := relayingOne(t, airMouse())
	restarted.restorePrepared()

	node := kernel.node(t, "/dev/input/event5")
	if !node.delivers(evKey) {
		t.Error("the restored demand does not deliver the class the claim asked for")
	}
	if node.delivers(evRel) {
		t.Error("the restored demand delivers a class the claim did not ask for")
	}
}

// The classes a controller advertises are the union of what its nodes
// carry, because a DualSense is a gamepad and an accelerometer and a
// touchpad on three separate nodes.
func TestRelayClassesUnionEveryNodeOfAController(t *testing.T) {
	kernel := newFakeKernel()
	kernel.registerNode("/dev/input/event5", dualSenseGamepad())
	kernel.registerNode("/dev/input/event6", dualSenseMotion())
	kernel.registerNode("/dev/input/event7", dualSenseTouchpad())
	held := newRelays(kernel)
	t.Cleanup(func() { held.stop(testMAC) })

	held.ensure(testMAC, []string{"/dev/input/event5", "/dev/input/event6", "/dev/input/event7"})

	want := classJoystick | classAccelerometer | classTouchpad
	if got := held.classes(testMAC); got != want {
		t.Errorf("classes = %s, want %s", got, want)
	}
}

// A controller that connected once still advertises its classes while
// it sleeps, because the relay creates its virtual devices from the
// snapshot in its bond's Secret and the classes come from the same
// capabilities.
func TestRelayClassesComeBackFromTheStoredSnapshot(t *testing.T) {
	kernel := newFakeKernel()
	kernel.registerNode("/dev/input/event5", dualSenseGamepad())
	first := newRelays(kernel)
	first.ensure(testMAC, []string{"/dev/input/event5"})
	stored := first.snapshot(testMAC)
	first.stop(testMAC)

	// A new pod, with the controller asleep and no real node anywhere.
	restarted := newRelays(newFakeKernel())
	t.Cleanup(func() { restarted.stop(testMAC) })
	restarted.restore(testMAC, stored)

	if got := restarted.classes(testMAC); got != classJoystick {
		t.Errorf("classes = %s, want joystick", got)
	}
}

// A bond that has never connected has no node, so it carries no class
// and the slice publishes none for it.
func TestRelayClassesAreNoneForABondThatHasNeverConnected(t *testing.T) {
	held := newRelays(newFakeKernel())
	if got := held.classes(testMAC); got != 0 {
		t.Errorf("classes = %s, want none", got)
	}
}
