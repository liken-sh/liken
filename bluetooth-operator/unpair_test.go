package main

import (
	"testing"
)

// deletePeripheral sets deletionTimestamp on the Peripheral, as the API
// server does for a delete when a finalizer is present.
func deletePeripheral(t *testing.T, fixture *apiFixture) {
	t.Helper()
	peripheral := read[Peripheral](t, fixture, testPeripheralPath())
	peripheral.Metadata.DeletionTimestamp = timestamp(testNow)
	fixture.put(t, testPeripheralPath(), peripheral)
}

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
	// The device stays in the slice because the claim is not released.
	if len(pass.excluded) != 0 {
		t.Errorf("excluded = %v, want the device still in the slice", pass.excluded)
	}
	if pass.again == 0 {
		t.Error("pass.again is 0, want a follow-up pass while the teardown waits")
	}
}

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

	if len(pass.excluded) != 0 {
		t.Errorf("excluded = %v, want the device still in the slice", pass.excluded)
	}
	if radio.called("Remove") {
		t.Errorf("the bond went while a claim held the controller: %v", radio.calls)
	}
	if _, found := fixture.objects[testPeripheralPath()]; !found {
		t.Error("the Peripheral lost its finalizer while a claim held the controller, want it kept")
	}
}

func TestUnpairRemovesTheDeviceFromTheSliceThenRemovesTheBond(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Connected = false
	radio := testRadio(t, device)
	inventory := testInventory(t, fixture, radio)
	inventory.reconcile()
	deletePeripheral(t, fixture)

	removal := inventory.reconcile()
	if !removal.excluded["a0:ab:51:33:b7:12"] {
		t.Fatalf("excluded = %v, want the device left out of the slice", removal.excluded)
	}
	if radio.called("Remove") {
		t.Fatalf("the bond went before the device left the slice: %v", radio.calls)
	}

	// The publisher has written the slice without the device.
	inventory.published()
	inventory.reconcile()

	if !radio.called("Remove a0-ab-51-33-b7-12") {
		t.Fatalf("the bond was never removed: %v", radio.calls)
	}
	if _, found := fixture.objects[testPeripheralPath()]; found {
		t.Error("the Peripheral still exists after the bond was removed, want its finalizer released")
	}
	if _, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_peripheral_connected", map[string]string{"peripheral": "a0-ab-51-33-b7-12"}); found {
		t.Error("bluetooth_peripheral_connected still has a series for the Peripheral after the unpair, want none")
	}
}

// When the slice write fails, the slice still lists the device, so the
// bond stays until a write succeeds.
func TestUnpairWaitsForTheSliceWriteBeforeRemovingTheBond(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Connected = false
	radio := testRadio(t, device)
	inventory := testInventory(t, fixture, radio)
	inventory.reconcile()
	deletePeripheral(t, fixture)

	inventory.reconcile()
	// The test does not call published(), which models a failed slice
	// write.
	pass := inventory.reconcile()

	if radio.called("Remove") {
		t.Fatalf("the bond went while the slice still published the device: %v", radio.calls)
	}
	if !pass.excluded["a0:ab:51:33:b7:12"] {
		t.Errorf("excluded = %v, want the device still left out of the slice", pass.excluded)
	}
}

// The teardown does not write the bond's Secret. Garbage collection
// deletes it with the Peripheral that owns it.
func TestUnpairLeavesTheSecretToTheOwnerReference(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Connected = false
	inventory := testInventory(t, fixture, testRadio(t, device))
	inventory.reconcile()
	deletePeripheral(t, fixture)

	pass := inventory.reconcile()

	if !pass.unpairing[testAddress(t, testDevice)] {
		t.Errorf("unpairing = %v, want the bond listed", pass.unpairing)
	}
	if _, owned := pass.owners[testAddress(t, testDevice)]; owned {
		t.Errorf("owners = %v, want no owner for a bond under teardown", pass.owners)
	}
}

// The relay stops in the step that removes the device from the slice.
// No claim holds the device by then, so no pod loses its input node.
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
	removal := inventory.reconcile()

	if !removal.excluded["a0:ab:51:33:b7:12"] {
		t.Fatalf("excluded = %v, want the device left out of the slice", removal.excluded)
	}
	if nodes := inventory.relays.virtualNodes("a0:ab:51:33:b7:12"); len(nodes) != 0 {
		t.Errorf("the relay still has virtual nodes %v, want none", nodes)
	}
}
