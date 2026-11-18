package main

import (
	"testing"
)

func TestReconcileCreatesTheAdapterForTheRadioItUses(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t))

	pass := inventory.reconcile()

	if !pass.ok {
		t.Fatal("the pass reported a failure")
	}
	adapter := read[Adapter](t, fixture, testAdapterObjectPath())
	if adapter.Metadata.Name != testAdapterName {
		t.Errorf("the Adapter is named %q, want %q", adapter.Metadata.Name, testAdapterName)
	}
	if !adapter.Metadata.hasFinalizer(adapterFinalizer) {
		t.Errorf("the finalizers are %v, want %s", adapter.Metadata.Finalizers, adapterFinalizer)
	}
	if adapter.Status.Address != testAdapter || adapter.Status.Node != "liken-1" || !adapter.Status.Powered {
		t.Errorf("the status is %+v, want address %s, node liken-1, and powered", adapter.Status, testAdapter)
	}
	// A reinstall gives the Node a new UID, so garbage collection would
	// delete an Adapter that the old Node owned.
	if len(adapter.Metadata.OwnerReferences) != 0 {
		t.Errorf("the ownerReferences are %+v, want none", adapter.Metadata.OwnerReferences)
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
			t.Errorf("the requests are %v, want no POST for an Adapter that already exists", fixture.requests)
		}
	}
	// The radio moved to this machine, so status.node follows it.
	adapter := read[Adapter](t, fixture, testAdapterObjectPath())
	if adapter.Status.Node != "liken-1" || !adapter.Status.Powered {
		t.Errorf("the status is %+v, want node liken-1 and powered", adapter.Status)
	}
}

// An Adapter created by hand has no finalizer, so the pass patches one
// on and then writes the status. Both writes are conditional on the
// resourceVersion, and the patch changes it, so the status write must
// use the version that the patch returned.
func TestReconcileAddsTheFinalizerToAnAdapterWithoutOne(t *testing.T) {
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
	if !adapter.Metadata.hasFinalizer(adapterFinalizer) {
		t.Errorf("the finalizers are %v, want %s", adapter.Metadata.Finalizers, adapterFinalizer)
	}
	if adapter.Status.Node != "liken-1" {
		t.Errorf("the status is %+v, want node liken-1, written after the finalizer patch", adapter.Status)
	}
}

func TestReconcileWritesTheAliasToTheRadio(t *testing.T) {
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
		t.Fatalf("the radio calls are %v, want SetAdapterAlias liken-1-living-room", radio.calls)
	}
	// The radio already has the alias, so the next pass does not write
	// it.
	radio.calls = nil
	inventory.reconcile()
	if radio.called("SetAdapterAlias") {
		t.Errorf("the radio calls are %v, want no SetAdapterAlias because the radio already has the alias", radio.calls)
	}
}

// A new bluetoothd starts the adapter with page scan off, and a radio
// that is not connectable ignores bonded devices that try to
// reconnect. The pass sets Connectable to true only when it is false.
func TestReconcileMakesTheAdapterConnectable(t *testing.T) {
	fixture := newAPIFixture()
	radio := testRadio(t)
	radio.snapshot.Adapter.Connectable = false
	inventory := testInventory(t, fixture, radio)

	inventory.reconcile()

	if !radio.called("SetAdapterConnectable true") {
		t.Fatalf("the radio calls are %v, want SetAdapterConnectable true", radio.calls)
	}
	// The radio is connectable now, so the next pass does not write it.
	radio.calls = nil
	inventory.reconcile()
	if radio.called("SetAdapterConnectable") {
		t.Errorf("the radio calls are %v, want no SetAdapterConnectable because the radio is already connectable", radio.calls)
	}
}

// The pass does not set Connectable on an unpowered adapter, because
// bluetoothd rejects property writes on a powered-off adapter. The
// pass waits for AutoEnable to power it on.
func TestReconcileLeavesAnUnpoweredAdapterAlone(t *testing.T) {
	fixture := newAPIFixture()
	radio := testRadio(t)
	radio.snapshot.Adapter.Powered = false
	radio.snapshot.Adapter.Connectable = false
	inventory := testInventory(t, fixture, radio)

	inventory.reconcile()

	if radio.called("SetAdapterConnectable") {
		t.Errorf("the radio calls are %v, want no SetAdapterConnectable on an adapter that is powered off", radio.calls)
	}
}

// Deleting the Adapter of a present radio would cascade to every
// Peripheral under it and every bond Secret under those, which would
// unpair working controllers.
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
	if !adapter.Metadata.hasFinalizer(adapterFinalizer) {
		t.Fatal("the Adapter has lost its finalizer, want it kept because its radio is present")
	}
	if adapter.Status.DeletionRefused == "" {
		t.Error("status.deletionRefused is empty, want the reason the Adapter stays")
	}
}

// When the radio is gone, the pass removes the finalizer so that the
// deletion completes and deletes the Peripherals and their Secrets.
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
		t.Fatalf("the Adapter of a radio that is gone still exists, want it deleted after its finalizer is removed: %+v",
			read[Adapter](t, fixture, testAdapterObjectPath()).Metadata)
	}
}

// Only the machine in status.node releases an Adapter. A radio moved
// to another machine is adopted there, and this operator must not
// remove the finalizer from it.
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
		t.Fatal("the Adapter is gone, want it kept because status.node names another machine")
	}
}
