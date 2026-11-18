package main

import (
	"testing"
)

func testPeripheralPath() string { return peripheralPath("a0-ab-51-33-b7-12") }

func TestReconcileAdoptsEveryBondAsAPeripheral(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))

	pass := inventory.reconcile()

	if !pass.ok {
		t.Fatal("the pass reported a failure, want it to succeed")
	}
	peripheral := read[Peripheral](t, fixture, testPeripheralPath())
	// The Adapter owns the Peripheral, so deleting an Adapter whose radio
	// is gone also deletes every Peripheral of that radio.
	if len(peripheral.Metadata.OwnerReferences) != 1 ||
		peripheral.Metadata.OwnerReferences[0].Kind != adapterKind ||
		peripheral.Metadata.OwnerReferences[0].Name != testAdapterName {
		t.Errorf("ownerReferences = %+v, want one reference to the Adapter %s", peripheral.Metadata.OwnerReferences, testAdapterName)
	}
	if !peripheral.Metadata.hasFinalizer(peripheralFinalizer) {
		t.Errorf("finalizers = %v, want %s", peripheral.Metadata.Finalizers, peripheralFinalizer)
	}
	if peripheral.Status.Address != testDevice || peripheral.Status.Adapter != testAdapter {
		t.Errorf("status = %+v, want address %s and adapter %s", peripheral.Status, testDevice, testAdapter)
	}
	if peripheral.Status.Node != "liken-1" {
		t.Errorf("status.node = %q, want the adapter's node", peripheral.Status.Node)
	}
	if peripheral.Status.Name != "DualSense Wireless Controller" {
		t.Errorf("status.name = %q, want the name the device reports", peripheral.Status.Name)
	}
	if !peripheral.Status.Bond.Held {
		t.Errorf("status.bond = %+v, want held", peripheral.Status.Bond)
	}
	if reason(peripheral, conditionConnected) != reasonLinkUp {
		t.Errorf("conditions = %+v, want Connected with the reason %s", peripheral.Status.Conditions, reasonLinkUp)
	}
	if peripheral.Status.Bond.Secret != "liken-system/bluetooth-bond-a0-ab-51-33-b7-12" {
		t.Errorf("status.bond.secret = %q, want liken-system/bluetooth-bond-a0-ab-51-33-b7-12", peripheral.Status.Bond.Secret)
	}
	if peripheral.Status.Bond.PairedAt != timestamp(testNow) {
		t.Errorf("status.bond.pairedAt = %q, want %q", peripheral.Status.Bond.PairedAt, timestamp(testNow))
	}
	// The bond store sets this Peripheral as the owner of the bond's
	// Secret, and reads it from pass.owners.
	owner, owned := pass.owners[testAddress(t, testDevice)]
	if !owned || owner.Name != "a0-ab-51-33-b7-12" || owner.UID == "" {
		t.Errorf("owners = %+v, want one for a0-ab-51-33-b7-12 with a UID", pass.owners)
	}
}

// A device that the radio has only discovered has no bond, so the
// operator creates no Peripheral for it.
func TestReconcileAdoptsNothingForADeviceWithNoBond(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, seenDevice(t, testDevice, "Somebody's Phone")))

	inventory.reconcile()

	if _, found := fixture.objects[testPeripheralPath()]; found {
		t.Fatal("a device with no bond got a Peripheral, want none")
	}
}

// A Peripheral whose bond is gone from bluetoothd stays, and its status
// reports the missing bond, because deleting a Peripheral unpairs the
// device.
func TestReconcileReportsABondThatLeftBluetoothd(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))
	inventory.reconcile()

	inventory.radio.(*fakeRadio).snapshot.Devices = nil
	inventory.reconcile()

	peripheral := read[Peripheral](t, fixture, testPeripheralPath())
	bond := peripheral.Status.Bond
	if bond.Held || bond.Paired || bond.Bonded || bond.Trusted || bond.Connected {
		t.Errorf("status.bond = %+v, want every flag false", bond)
	}
	if reason(peripheral, conditionConnected) != reasonNotBonded {
		t.Errorf("conditions = %+v, want Connected with the reason %s", peripheral.Status.Conditions, reasonNotBonded)
	}
}

func TestReconcileSetsTrustedAndAliasOnTheDevice(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Trusted = false
	radio := testRadio(t, device)
	inventory := testInventory(t, fixture, radio)
	inventory.reconcile()

	// A person sets an alias and asks for the controller to be trusted.
	peripheral := read[Peripheral](t, fixture, testPeripheralPath())
	trusted := true
	peripheral.Spec = PeripheralSpec{Alias: "player-one-pad", Trusted: &trusted}
	fixture.put(t, testPeripheralPath(), peripheral)

	radio.calls = nil
	inventory.reconcile()

	if !radio.called("SetDeviceTrusted a0-ab-51-33-b7-12 true") {
		t.Errorf("radio calls are %v, want SetDeviceTrusted true", radio.calls)
	}
	if !radio.called("SetDeviceAlias a0-ab-51-33-b7-12 player-one-pad") {
		t.Errorf("radio calls are %v, want SetDeviceAlias player-one-pad", radio.calls)
	}

	// The device has both values now, so the next pass writes nothing.
	radio.calls = nil
	inventory.reconcile()
	if radio.called("SetDevice") {
		t.Errorf("a steady pass made the radio calls %v, want none", radio.calls)
	}
}

// The pass runs on every bus signal and every kernel event, so writing
// the status unconditionally would send an API request for each one.
func TestReconcileWritesAPeripheralStatusOnlyWhenItChanges(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))
	inventory.reconcile()

	fixture.requests = nil
	inventory.reconcile()

	for _, request := range fixture.requests {
		if request == "PUT "+testPeripheralPath()+"/status" {
			t.Fatalf("a steady pass sent %v, want no status write", fixture.requests)
		}
	}
}

// Adoption copies spec.trusted from the device and does not change the
// device.
func TestAdoptionCopiesTrustedFromTheDevice(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Trusted = false
	radio := testRadio(t, device)
	inventory := testInventory(t, fixture, radio)

	inventory.reconcile()

	peripheral := read[Peripheral](t, fixture, testPeripheralPath())
	if peripheral.Spec.Trusted == nil || *peripheral.Spec.Trusted {
		t.Errorf("spec.trusted = %v, want the device's own false", peripheral.Spec.Trusted)
	}
	if radio.called("SetDeviceTrusted") {
		t.Errorf("adoption made the radio calls %v, want none", radio.calls)
	}
}

// reason returns the reason of the status condition with the given
// type, or "" when the status has no such condition.
func reason(peripheral *Peripheral, conditionType string) string {
	for _, condition := range peripheral.Status.Conditions {
		if condition.Type == conditionType {
			return condition.Reason
		}
	}
	return ""
}

// The battery level and the icon come from the GetManagedObjects read
// that the pass already makes.
func TestPeripheralStatusReportsTheBatteryAndTheIcon(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Icon = "input-gaming"
	device.Battery = &deviceBattery{Percentage: 62, Source: "HID"}
	inventory := testInventory(t, fixture, testRadio(t, device))

	inventory.reconcile()

	peripheral := read[Peripheral](t, fixture, testPeripheralPath())
	if peripheral.Status.Icon != "input-gaming" {
		t.Errorf("status.icon = %q, want input-gaming", peripheral.Status.Icon)
	}
	if peripheral.Status.Battery == nil {
		t.Fatalf("status = %+v, want a battery for a device that reports a level", peripheral.Status)
	}
	if peripheral.Status.Battery.Percentage != 62 || peripheral.Status.Battery.Source != "HID" {
		t.Errorf("status.battery = %+v, want 62 percent from HID", peripheral.Status.Battery)
	}
}

// When the kernel and BlueZ both report a level, the kernel's is used,
// because it also reports whether the battery is charging.
func TestPeripheralStatusPrefersTheKernelBattery(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Battery = &deviceBattery{Percentage: 88, Source: "GATT Battery Service"}
	inventory := testInventory(t, fixture, testRadio(t, device))
	sysfsFor(t, withBattery(
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"), "40", "Discharging"))

	inventory.reconcile()

	peripheral := read[Peripheral](t, fixture, testPeripheralPath())
	battery := peripheral.Status.Battery
	if battery == nil {
		t.Fatalf("status = %+v, want a battery", peripheral.Status)
	}
	if battery.Percentage != 40 {
		t.Errorf("status.battery.percentage = %d, want the kernel's 40", battery.Percentage)
	}
	if battery.Source != "ps-controller-battery-a0:ab:51:33:b7:12" {
		t.Errorf("status.battery.source = %q, want the kernel power supply ps-controller-battery-a0:ab:51:33:b7:12", battery.Source)
	}
	if battery.Charging == nil || *battery.Charging {
		t.Errorf("status.battery.charging = %v, want false", battery.Charging)
	}
}

// BlueZ reads a Low Energy device's level from its GATT battery
// service, and the kernel registers no power supply for it, so the
// status uses Battery1.
func TestPeripheralStatusFallsBackToBlueZ(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Battery = &deviceBattery{Percentage: 88, Source: "GATT Battery Service"}
	inventory := testInventory(t, fixture, testRadio(t, device))
	sysfsFor(t, dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"))

	inventory.reconcile()

	battery := read[Peripheral](t, fixture, testPeripheralPath()).Status.Battery
	if battery == nil {
		t.Fatal("status.battery is absent, want the level that BlueZ reports")
	}
	if battery.Percentage != 88 || battery.Source != "GATT Battery Service" {
		t.Errorf("status.battery = %+v, want 88 percent from the GATT Battery Service", battery)
	}
	if battery.Charging != nil {
		t.Errorf("status.battery.charging = %v, want none", *battery.Charging)
	}
}

// Most controllers report no level, and a status.battery of zero would
// read as an empty battery.
func TestPeripheralStatusOmitsTheBatteryWhenTheDeviceReportsNone(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))

	inventory.reconcile()

	peripheral := read[Peripheral](t, fixture, testPeripheralPath())
	if peripheral.Status.Battery != nil {
		t.Errorf("status.battery = %+v, want none", peripheral.Status.Battery)
	}
}

// status.bond copies the Paired, Bonded, Trusted, and Connected
// properties of the device in BlueZ.
func TestPeripheralBondCopiesTheDevicePropertiesFromBlueZ(t *testing.T) {
	cases := []struct {
		name   string
		change func(*deviceState)
		want   BondStatus
	}{
		{
			name: "a bonded controller on the air",
			want: BondStatus{Held: true, Paired: true, Bonded: true, Trusted: true, Connected: true},
		},
		{
			name:   "the same controller with its link down",
			change: func(device *deviceState) { device.Connected = false },
			want:   BondStatus{Held: true, Paired: true, Bonded: true, Trusted: true},
		},
		{
			name:   "a controller BlueZ does not trust",
			change: func(device *deviceState) { device.Trusted = false },
			want:   BondStatus{Held: true, Paired: true, Bonded: true, Connected: true},
		},
		{
			name:   "a pairing that stored no link key",
			change: func(device *deviceState) { device.Bonded = false },
			want:   BondStatus{Held: true, Paired: true, Trusted: true, Connected: true},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newAPIFixture()
			device := pairedDevice(t, testDevice)
			if c.change != nil {
				c.change(&device)
			}
			inventory := testInventory(t, fixture, testRadio(t, device))

			inventory.reconcile()

			bond := read[Peripheral](t, fixture, testPeripheralPath()).Status.Bond
			bond.Secret, bond.PairedAt, bond.Request = "", "", ""
			if bond != c.want {
				t.Errorf("status.bond = %+v, want %+v", bond, c.want)
			}
		})
	}
}
