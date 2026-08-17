package main

// These tests cover the Pairing object: adoption of the bonds
// bluetoothd already holds, the status that reports the radio's
// state, and the spec that reconciles back into the device.

import (
	"testing"
)

// testPairingPath is where the test device's Pairing lives.
func testPairingPath() string { return pairingPath("a0-ab-51-33-b7-12") }

func TestReconcileAdoptsEveryBondAsAPairing(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))

	pass := inventory.reconcile()

	if !pass.ok {
		t.Fatal("the pass reported a failure")
	}
	pairing := read[Pairing](t, fixture, testPairingPath())
	// The Adapter owns the Pairing, so retiring a dead radio is one
	// delete that collects every bond keyed to it.
	if len(pairing.Metadata.OwnerReferences) != 1 ||
		pairing.Metadata.OwnerReferences[0].Kind != adapterKind ||
		pairing.Metadata.OwnerReferences[0].Name != testAdapterName {
		t.Errorf("ownerReferences = %+v", pairing.Metadata.OwnerReferences)
	}
	if !pairing.Metadata.holds(pairingFinalizer) {
		t.Errorf("finalizers = %v", pairing.Metadata.Finalizers)
	}
	if pairing.Status.Address != testDevice || pairing.Status.Adapter != testAdapter {
		t.Errorf("status = %+v", pairing.Status)
	}
	if !pairing.Status.Bonded || !pairing.Status.Connected {
		t.Errorf("status = %+v", pairing.Status)
	}
	if pairing.Status.Secret != "liken-system/bluetooth-bond-a0-ab-51-33-b7-12" {
		t.Errorf("status.secret = %q", pairing.Status.Secret)
	}
	if pairing.Status.PairedAt != timestamp(testNow) {
		t.Errorf("status.pairedAt = %q", pairing.Status.PairedAt)
	}
	// The bond's Secret is owned by this Pairing, and the bond store
	// reads that from the pass.
	owner, owned := pass.owners[testAddress(t, testDevice)]
	if !owned || owner.Name != "a0-ab-51-33-b7-12" || owner.UID == "" {
		t.Errorf("owners = %+v", pass.owners)
	}
}

// A device the radio has only observed holds no link key, so this
// machine must not publish it and it gets no Pairing.
func TestReconcileAdoptsNothingForADeviceWithNoBond(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, seenDevice(t, testDevice, "Somebody's Phone")))

	inventory.reconcile()

	if _, found := fixture.objects[testPairingPath()]; found {
		t.Fatal("a device with no bond got a Pairing")
	}
}

// A Pairing whose bond is gone from bluetoothd keeps its object. The
// operator reports the gap, and a person decides, because deleting a
// Pairing is the unpair API.
func TestReconcileReportsABondThatLeftBluetoothd(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))
	inventory.reconcile()

	inventory.radio.(*fakeRadio).snapshot.Devices = nil
	inventory.reconcile()

	pairing := read[Pairing](t, fixture, testPairingPath())
	if pairing.Status.Bonded || pairing.Status.Connected {
		t.Errorf("status = %+v", pairing.Status)
	}
}

func TestReconcileCarriesTrustedAndAliasIntoTheDevice(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Trusted = false
	radio := testRadio(t, device)
	inventory := testInventory(t, fixture, radio)
	inventory.reconcile()

	// A person names the controller and asks for it to be trusted.
	pairing := read[Pairing](t, fixture, testPairingPath())
	trusted := true
	pairing.Spec = PairingSpec{Alias: "player-one-pad", Trusted: &trusted}
	fixture.put(t, testPairingPath(), pairing)

	radio.calls = nil
	inventory.reconcile()

	if !radio.called("SetDeviceTrusted a0-ab-51-33-b7-12 true") {
		t.Errorf("trusted never reached the device: %v", radio.calls)
	}
	if !radio.called("SetDeviceAlias a0-ab-51-33-b7-12 player-one-pad") {
		t.Errorf("the alias never reached the device: %v", radio.calls)
	}

	// The device holds both now, so a steady pass writes nothing.
	radio.calls = nil
	inventory.reconcile()
	if radio.called("SetDevice") {
		t.Errorf("a steady pass wrote to the device again: %v", radio.calls)
	}
}

// The pass runs on every bus signal and every kernel event, and an
// unconditional status write would send a request to the API server
// on each of them.
func TestReconcileWritesAPairingStatusOnlyWhenItChanges(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))
	inventory.reconcile()

	fixture.requests = nil
	inventory.reconcile()

	for _, request := range fixture.requests {
		if request == "PUT "+statusPath(testPairingPath()) {
			t.Fatalf("a steady pass rewrote the status: %v", fixture.requests)
		}
	}
}

// spec.trusted starts from what bluetoothd already holds, so adoption
// states the device's real state rather than asserting a default onto
// hardware that is working.
func TestAdoptionTakesTrustedFromTheDevice(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Trusted = false
	radio := testRadio(t, device)
	inventory := testInventory(t, fixture, radio)

	inventory.reconcile()

	pairing := read[Pairing](t, fixture, testPairingPath())
	if pairing.Spec.Trusted == nil || *pairing.Spec.Trusted {
		t.Errorf("spec.trusted = %v, want the device's own false", pairing.Spec.Trusted)
	}
	if radio.called("SetDeviceTrusted") {
		t.Errorf("adoption changed the device: %v", radio.calls)
	}
}
