package main

// These tests cover the Adapter object's lifecycle: the operator
// creates one for the radio it holds, adopts one that is already
// there, writes spec.alias into the radio, refuses a deletion while
// the radio is present, and lets a deletion through once the radio is
// gone.

import (
	"testing"
)

func TestReconcileCreatesTheAdapterForTheRadioItHolds(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t))

	pass := inventory.reconcile()

	if !pass.ok {
		t.Fatal("the pass reported a failure")
	}
	adapter := read[Adapter](t, fixture, testAdapterObjectPath())
	// The object is named for the radio, so it follows the radio and
	// not the machine.
	if adapter.Metadata.Name != testAdapterName {
		t.Errorf("name = %q", adapter.Metadata.Name)
	}
	if !adapter.Metadata.holds(adapterFinalizer) {
		t.Errorf("finalizers = %v", adapter.Metadata.Finalizers)
	}
	if adapter.Status.Address != testAdapter || adapter.Status.Node != "liken-1" || !adapter.Status.Powered {
		t.Errorf("status = %+v", adapter.Status)
	}
	// Nothing owns the Adapter. An owner reference binds to one UID,
	// and a Node registered again after a reinstall has a new one,
	// which would sweep every bond under this radio.
	if len(adapter.Metadata.OwnerReferences) != 0 {
		t.Errorf("ownerReferences = %+v", adapter.Metadata.OwnerReferences)
	}
}

func TestReconcileAdoptsAnAdapterThatIsAlreadyThere(t *testing.T) {
	fixture := newAPIFixture()
	fixture.put(t, testAdapterObjectPath(), &Adapter{
		APIVersion: pairingAPI,
		Kind:       adapterKind,
		Metadata:   ObjectMeta{Name: testAdapterName, Finalizers: []string{adapterFinalizer}},
		Status:     AdapterStatus{Address: testAdapter, Node: "liken-2", Powered: false},
	})
	inventory := testInventory(t, fixture, testRadio(t))

	pass := inventory.reconcile()

	if !pass.ok {
		t.Fatal("the pass reported a failure")
	}
	for _, request := range fixture.requests {
		if request == "POST "+adaptersPath() {
			t.Errorf("an adapter that was already there was created again: %v", fixture.requests)
		}
	}
	// The radio moved to this machine, and status reports where the
	// radio is now.
	adapter := read[Adapter](t, fixture, testAdapterObjectPath())
	if adapter.Status.Node != "liken-1" || !adapter.Status.Powered {
		t.Errorf("status = %+v", adapter.Status)
	}
}

// An Adapter a person created by hand has no finalizer, so the pass
// patches one on and then writes the status. Both writes are
// conditional on the object's version, and the patch moves it, so the
// status write has to include the version the patch produced.
func TestReconcileHoldsAnAdapterThatCarriesNoFinalizer(t *testing.T) {
	fixture := newAPIFixture()
	fixture.put(t, testAdapterObjectPath(), &Adapter{
		APIVersion: pairingAPI,
		Kind:       adapterKind,
		Metadata:   ObjectMeta{Name: testAdapterName},
	})
	inventory := testInventory(t, fixture, testRadio(t))

	pass := inventory.reconcile()

	if !pass.ok {
		t.Fatal("the pass reported a failure")
	}
	adapter := read[Adapter](t, fixture, testAdapterObjectPath())
	if !adapter.Metadata.holds(adapterFinalizer) {
		t.Errorf("finalizers = %v", adapter.Metadata.Finalizers)
	}
	if adapter.Status.Node != "liken-1" {
		t.Errorf("status = %+v, want the status written after the patch", adapter.Status)
	}
}

func TestReconcileCarriesTheAliasIntoTheRadio(t *testing.T) {
	fixture := newAPIFixture()
	fixture.put(t, testAdapterObjectPath(), &Adapter{
		APIVersion: pairingAPI,
		Kind:       adapterKind,
		Metadata:   ObjectMeta{Name: testAdapterName, Finalizers: []string{adapterFinalizer}},
		Spec:       AdapterSpec{Alias: "liken-1-living-room"},
	})
	radio := testRadio(t)
	inventory := testInventory(t, fixture, radio)

	inventory.reconcile()

	if !radio.called("SetAdapterAlias liken-1-living-room") {
		t.Fatalf("the alias never reached the radio: %v", radio.calls)
	}
	// The radio has the alias now, so the next pass has nothing
	// to write.
	radio.calls = nil
	inventory.reconcile()
	if radio.called("SetAdapterAlias") {
		t.Errorf("the alias was written again: %v", radio.calls)
	}
}

// A fresh bluetoothd starts the adapter with page scan off, and a
// radio that is not connectable answers no bonded device's reconnect.
// The pass asserts the setting on, and only when it diverges.
func TestReconcileMakesTheAdapterConnectable(t *testing.T) {
	fixture := newAPIFixture()
	radio := testRadio(t)
	radio.snapshot.Adapter.Connectable = false
	inventory := testInventory(t, fixture, radio)

	inventory.reconcile()

	if !radio.called("SetAdapterConnectable true") {
		t.Fatalf("the radio was left unconnectable: %v", radio.calls)
	}
	// The radio is connectable now, so the next pass writes nothing.
	radio.calls = nil
	inventory.reconcile()
	if radio.called("SetAdapterConnectable") {
		t.Errorf("connectable was written again: %v", radio.calls)
	}
}

// An unpowered adapter takes no connectable write. bluetoothd refuses
// property writes on a downed adapter, and AutoEnable is what powers
// it, so the pass waits for that instead of writing into the refusal.
func TestReconcileLeavesADownedAdapterAlone(t *testing.T) {
	fixture := newAPIFixture()
	radio := testRadio(t)
	radio.snapshot.Adapter.Powered = false
	radio.snapshot.Adapter.Connectable = false
	inventory := testInventory(t, fixture, radio)

	inventory.reconcile()

	if radio.called("SetAdapterConnectable") {
		t.Errorf("a downed adapter took a connectable write: %v", radio.calls)
	}
}

// A delete against live hardware would cascade to every Peripheral
// under this Adapter and to every bond Secret under those, which is a
// mass unpair of controllers that are working.
func TestReconcileRefusesToDeleteAnAdapterWhoseRadioIsPresent(t *testing.T) {
	fixture := newAPIFixture()
	fixture.put(t, testAdapterObjectPath(), &Adapter{
		APIVersion: pairingAPI,
		Kind:       adapterKind,
		Metadata: ObjectMeta{
			Name:              testAdapterName,
			Finalizers:        []string{adapterFinalizer},
			DeletionTimestamp: timestamp(testNow),
		},
		Status: AdapterStatus{Address: testAdapter, Node: "liken-1", Powered: true},
	})
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))

	inventory.reconcile()

	adapter := read[Adapter](t, fixture, testAdapterObjectPath())
	if !adapter.Metadata.holds(adapterFinalizer) {
		t.Fatal("the operator released an Adapter whose radio is present")
	}
	if adapter.Status.DeletionRefused == "" {
		t.Error("the refusal is not in the status, so nothing reports why the object stays")
	}
}

// The radio is gone, which is the cleanup path: the Adapter's deletion
// runs, and the cascade takes its Peripherals and their Secrets.
func TestReconcileReleasesAnAdapterWhoseRadioIsGone(t *testing.T) {
	fixture := newAPIFixture()
	fixture.put(t, testAdapterObjectPath(), &Adapter{
		APIVersion: pairingAPI,
		Kind:       adapterKind,
		Metadata: ObjectMeta{
			Name:              testAdapterName,
			Finalizers:        []string{adapterFinalizer},
			DeletionTimestamp: timestamp(testNow),
		},
		Status: AdapterStatus{Address: testAdapter, Node: "liken-1"},
	})
	radio := testRadio(t)
	radio.err = ErrNoAdapter
	inventory := testInventory(t, fixture, radio)

	inventory.reconcile()

	if _, found := fixture.objects[testAdapterObjectPath()]; found {
		t.Fatalf("the Adapter for a radio that is gone kept its finalizer: %+v",
			read[Adapter](t, fixture, testAdapterObjectPath()).Metadata)
	}
}

// Only the machine that last held the radio releases its Adapter. A
// dongle carried to another machine is adopted there, and this one
// must not release an object that the other machine now holds.
func TestReconcileLeavesAnotherMachinesAdapterAlone(t *testing.T) {
	fixture := newAPIFixture()
	path := adapterPath("04-4a-69-66-92-27")
	fixture.put(t, path, &Adapter{
		APIVersion: pairingAPI,
		Kind:       adapterKind,
		Metadata: ObjectMeta{
			Name:              "04-4a-69-66-92-27",
			Finalizers:        []string{adapterFinalizer},
			DeletionTimestamp: timestamp(testNow),
		},
		Status: AdapterStatus{Address: "04:4A:69:66:92:27", Node: "liken-2"},
	})
	inventory := testInventory(t, fixture, testRadio(t))

	inventory.reconcile()

	if _, found := fixture.objects[path]; !found {
		t.Fatal("this operator released another machine's Adapter")
	}
}
