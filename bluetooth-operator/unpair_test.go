package main

// These tests cover the ordered teardown a deleted Peripheral runs. The
// order matters: a device a claim still names never leaves the
// published inventory, and the bond is removed only after the device
// leaves the slice.

import (
	"testing"
)

// deletePeripheral marks the Peripheral the way a kubectl delete does:
// the API server stamps a deletionTimestamp and keeps the object,
// because the operator's finalizer is on it.
func deletePeripheral(t *testing.T, fixture *apiFixture) {
	t.Helper()
	peripheral := read[Peripheral](t, fixture, testPeripheralPath())
	peripheral.Metadata.DeletionTimestamp = timestamp(testNow)
	fixture.put(t, testPeripheralPath(), peripheral)
}

// The first step ends the session. The controller then registers no
// evdev node, the ordinary reconcile taints the slice device, and the
// eviction controller ends the pod that holds the claim.
func TestUnpairDisconnectsBeforeAnythingElse(t *testing.T) {
	fixture := newAPIFixture()
	radio := testRadio(t, pairedDevice(t, testDevice))
	inventory := testInventory(t, fixture, radio)
	inventory.reconcile()
	deletePeripheral(t, fixture)

	radio.calls = nil
	pass := inventory.reconcile()

	if !radio.called("Disconnect a0-ab-51-33-b7-12") {
		t.Fatalf("the teardown did not disconnect the device: %v", radio.calls)
	}
	if radio.called("Remove") {
		t.Errorf("the bond went before the session ended: %v", radio.calls)
	}
	// The device is still published, because the claim has not been
	// released yet.
	if len(pass.keepOut) != 0 {
		t.Errorf("keepOut = %v, want the device still in the slice", pass.keepOut)
	}
	if pass.again == 0 {
		t.Error("the teardown asked for no follow-up pass")
	}
}

// An allocation that names a device in no slice strands the kubelet's
// prepare call, with no bound on the retry. So the device stays in the
// inventory for as long as a claim holds it.
func TestUnpairKeepsTheDeviceInTheSliceWhileAClaimHoldsIt(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Connected = false
	radio := testRadio(t, device)
	inventory := testInventory(t, fixture, radio)
	inventory.reconcile()
	deletePeripheral(t, fixture)
	prepareClaim(t, "0f1e2d3c-0000-4000-8000-000000000003", "a0-ab-51-33-b7-12", "/dev/input/event5")

	radio.calls = nil
	pass := inventory.reconcile()

	if len(pass.keepOut) != 0 {
		t.Errorf("keepOut = %v, want the device still in the slice", pass.keepOut)
	}
	if radio.called("Remove") {
		t.Errorf("the bond went while a claim held the controller: %v", radio.calls)
	}
	if _, found := fixture.objects[testPeripheralPath()]; !found {
		t.Error("the Peripheral was released while a claim held the controller")
	}
}

// With the claim released, the device leaves the slice first and the
// bond goes on the next pass. The two are separated by a pass because
// the publisher writes the slice after the inventory runs.
func TestUnpairRetiresTheDeviceThenRemovesTheBond(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Connected = false
	radio := testRadio(t, device)
	inventory := testInventory(t, fixture, radio)
	inventory.reconcile()
	deletePeripheral(t, fixture)

	retire := inventory.reconcile()
	if !retire.keepOut["a0:ab:51:33:b7:12"] {
		t.Fatalf("keepOut = %v, want the device retired", retire.keepOut)
	}
	if radio.called("Remove") {
		t.Fatalf("the bond went before the device left the slice: %v", radio.calls)
	}

	// The publisher wrote the slice without the device.
	inventory.published()
	inventory.reconcile()

	if !radio.called("Remove a0-ab-51-33-b7-12") {
		t.Fatalf("the bond was never removed: %v", radio.calls)
	}
	if _, found := fixture.objects[testPeripheralPath()]; found {
		t.Error("the Peripheral kept its finalizer after the bond was gone")
	}
}

// A slice write that failed leaves the device published, so the bond
// must stay until a write really lands.
func TestUnpairWaitsForTheSliceWriteBeforeRemovingTheBond(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Connected = false
	radio := testRadio(t, device)
	inventory := testInventory(t, fixture, radio)
	inventory.reconcile()
	deletePeripheral(t, fixture)

	inventory.reconcile()
	// published() is not called, which models a slice write that
	// failed.
	pass := inventory.reconcile()

	if radio.called("Remove") {
		t.Fatalf("the bond went while the slice still published the device: %v", radio.calls)
	}
	if !pass.keepOut["a0:ab:51:33:b7:12"] {
		t.Errorf("keepOut = %v, want the device still retired", pass.keepOut)
	}
}

// The bond's Secret is not rewritten while the teardown runs. It is
// collected with the Peripheral that owns it.
func TestUnpairLeavesTheSecretToTheOwnerReference(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Connected = false
	inventory := testInventory(t, fixture, testRadio(t, device))
	inventory.reconcile()
	deletePeripheral(t, fixture)

	pass := inventory.reconcile()

	if !pass.unpairing[testAddress(t, testDevice)] {
		t.Errorf("unpairing = %v, want the bond named", pass.unpairing)
	}
	if _, owned := pass.owners[testAddress(t, testDevice)]; owned {
		t.Errorf("owners = %v, want no owner for a bond under teardown", pass.owners)
	}
}

// The relay stops in the step that takes the device out of the slice.
// The claim was released one step earlier, so no consumer loses a node
// under it, and no virtual node outlives the bond it stands for.
func TestUnpairStopsTheRelayWhenTheDeviceLeavesTheSlice(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Connected = false
	inventory := testInventory(t, fixture, testRadio(t, device))
	inventory.relays.restore("a0:ab:51:33:b7:12", storedCapabilities(t))
	inventory.reconcile()
	deletePeripheral(t, fixture)

	if len(inventory.relays.virtualNodes("a0:ab:51:33:b7:12")) != 1 {
		t.Fatal("the controller had no virtual node before the teardown")
	}
	retire := inventory.reconcile()

	if !retire.keepOut["a0:ab:51:33:b7:12"] {
		t.Fatalf("keepOut = %v, want the device retired", retire.keepOut)
	}
	if nodes := inventory.relays.virtualNodes("a0:ab:51:33:b7:12"); len(nodes) != 0 {
		t.Errorf("the relay still holds %v", nodes)
	}
}
