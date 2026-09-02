package main

// These tests cover the Peripheral object: adoption of the bonds
// bluetoothd already holds, the status that reports the radio's
// state, and the spec that reconciles back into the device.

import (
	"testing"
	"time"
)

// testPeripheralPath is the path of the test device's Peripheral.
func testPeripheralPath() string { return peripheralPath("a0-ab-51-33-b7-12") }

func TestReconcileAdoptsEveryBondAsAPeripheral(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))

	pass := inventory.reconcile()

	if !pass.ok {
		t.Fatal("the pass reported a failure")
	}
	peripheral := read[Peripheral](t, fixture, testPeripheralPath())
	// The Adapter owns the Peripheral, so retiring a dead radio is one
	// delete that collects every bond keyed to it.
	if len(peripheral.Metadata.OwnerReferences) != 1 ||
		peripheral.Metadata.OwnerReferences[0].Kind != adapterKind ||
		peripheral.Metadata.OwnerReferences[0].Name != testAdapterName {
		t.Errorf("ownerReferences = %+v", peripheral.Metadata.OwnerReferences)
	}
	if !peripheral.Metadata.holds(peripheralFinalizer) {
		t.Errorf("finalizers = %v", peripheral.Metadata.Finalizers)
	}
	if peripheral.Status.Address != testDevice || peripheral.Status.Adapter != testAdapter {
		t.Errorf("status = %+v", peripheral.Status)
	}
	if peripheral.Status.Node != "liken-1" {
		t.Errorf("status.node = %q, want the adapter's node", peripheral.Status.Node)
	}
	if peripheral.Status.Name != "DualSense Wireless Controller" {
		t.Errorf("status.name = %q, want the name the device reports", peripheral.Status.Name)
	}
	if !peripheral.Status.Bond.Held {
		t.Errorf("status.bond = %+v", peripheral.Status.Bond)
	}
	if reason(peripheral, conditionConnected) != reasonLinkUp {
		t.Errorf("conditions = %+v", peripheral.Status.Conditions)
	}
	if peripheral.Status.Bond.Secret != "liken-system/bluetooth-bond-a0-ab-51-33-b7-12" {
		t.Errorf("status.bond.secret = %q", peripheral.Status.Bond.Secret)
	}
	if peripheral.Status.Bond.PairedAt != timestamp(testNow) {
		t.Errorf("status.bond.pairedAt = %q", peripheral.Status.Bond.PairedAt)
	}
	// The bond's Secret is owned by this Peripheral, and the bond store
	// reads that from the pass.
	owner, owned := pass.owners[testAddress(t, testDevice)]
	if !owned || owner.Name != "a0-ab-51-33-b7-12" || owner.UID == "" {
		t.Errorf("owners = %+v", pass.owners)
	}
}

// A device the radio has only observed holds no link key, so this
// machine must not publish it and it gets no Peripheral.
func TestReconcileAdoptsNothingForADeviceWithNoBond(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, seenDevice(t, testDevice, "Somebody's Phone")))

	inventory.reconcile()

	if _, found := fixture.objects[testPeripheralPath()]; found {
		t.Fatal("a device with no bond got a Peripheral")
	}
}

// A Peripheral whose bond is gone from bluetoothd keeps its object. The
// operator reports the gap, and a person decides, because deleting a
// Peripheral is the unpair API.
func TestReconcileReportsABondThatLeftBluetoothd(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))
	inventory.reconcile()

	inventory.radio.(*fakeRadio).snapshot.Devices = nil
	inventory.reconcile()

	peripheral := read[Peripheral](t, fixture, testPeripheralPath())
	if peripheral.Status.Bond.Held {
		t.Errorf("status.bond = %+v", peripheral.Status.Bond)
	}
	if reason(peripheral, conditionConnected) != reasonNotBonded {
		t.Errorf("conditions = %+v", peripheral.Status.Conditions)
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
	peripheral := read[Peripheral](t, fixture, testPeripheralPath())
	trusted := true
	peripheral.Spec = PeripheralSpec{Alias: "player-one-pad", Trusted: &trusted}
	fixture.put(t, testPeripheralPath(), peripheral)

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
func TestReconcileWritesAPeripheralStatusOnlyWhenItChanges(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))
	inventory.reconcile()

	fixture.requests = nil
	inventory.reconcile()

	for _, request := range fixture.requests {
		if request == "PUT "+statusPath(testPeripheralPath()) {
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

	peripheral := read[Peripheral](t, fixture, testPeripheralPath())
	if peripheral.Spec.Trusted == nil || *peripheral.Spec.Trusted {
		t.Errorf("spec.trusted = %v, want the device's own false", peripheral.Spec.Trusted)
	}
	if radio.called("SetDeviceTrusted") {
		t.Errorf("adoption changed the device: %v", radio.calls)
	}
}

// reason reads one condition's reason, or the empty string when the
// status holds no such condition.
func reason(peripheral *Peripheral, conditionType string) string {
	for _, condition := range peripheral.Status.Conditions {
		if condition.Type == conditionType {
			return condition.Reason
		}
	}
	return ""
}

// The level and the icon come out of the managed-objects read the pass
// already makes, so a charge BlueZ reports reaches the object with no
// second call.
func TestPeripheralStatusReportsTheBatteryAndTheIcon(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Icon = "input-gaming"
	device.Battery = &deviceBattery{Percentage: 62, Source: "HID"}
	inventory := testInventory(t, fixture, testRadio(t, device))

	inventory.reconcile()

	peripheral := read[Peripheral](t, fixture, testPeripheralPath())
	if peripheral.Status.Icon != "input-gaming" {
		t.Errorf("status.icon = %q", peripheral.Status.Icon)
	}
	if peripheral.Status.Battery == nil {
		t.Fatalf("status.battery is absent for a device that reports a level: %+v", peripheral.Status)
	}
	if peripheral.Status.Battery.Percentage != 62 || peripheral.Status.Battery.Source != "HID" {
		t.Errorf("status.battery = %+v", peripheral.Status.Battery)
	}
}

// The kernel's entry wins wherever both sources report a level, because
// it also carries the charging state.
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
		t.Fatalf("status.battery is absent: %+v", peripheral.Status)
	}
	if battery.Percentage != 40 {
		t.Errorf("status.battery.percentage = %d, want the kernel's 40", battery.Percentage)
	}
	if battery.Source != "ps-controller-battery-a0:ab:51:33:b7:12" {
		t.Errorf("status.battery.source = %q", battery.Source)
	}
	if battery.Charging == nil || *battery.Charging {
		t.Errorf("status.battery.charging = %v, want false", battery.Charging)
	}
}

// BlueZ reads the level of a Low Energy device from its GATT battery
// service, and the kernel registers no power supply for it, so Battery1
// is the fallback.
func TestPeripheralStatusFallsBackToBlueZ(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Battery = &deviceBattery{Percentage: 88, Source: "GATT Battery Service"}
	inventory := testInventory(t, fixture, testRadio(t, device))
	sysfsFor(t, dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"))

	inventory.reconcile()

	battery := read[Peripheral](t, fixture, testPeripheralPath()).Status.Battery
	if battery == nil {
		t.Fatalf("status.battery is absent for a device BlueZ reports a level for")
	}
	if battery.Percentage != 88 || battery.Source != "GATT Battery Service" {
		t.Errorf("status.battery = %+v", battery)
	}
	if battery.Charging != nil {
		t.Errorf("status.battery.charging = %v, want none", *battery.Charging)
	}
}

// Most controllers report no level at all, and a block that said zero
// for them would read as an empty battery.
func TestPeripheralStatusOmitsTheBatteryWhenTheDeviceReportsNone(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))

	inventory.reconcile()

	peripheral := read[Peripheral](t, fixture, testPeripheralPath())
	if peripheral.Status.Battery != nil {
		t.Errorf("status.battery = %+v, want none", peripheral.Status.Battery)
	}
}

// The reason separates a controller asleep between presses from one
// that is off the air, which is what a consumer reads to decide whether
// to wait or to report a problem.
func TestConnectedConditionReason(t *testing.T) {
	hidOverGATT := fullUUID("1812")
	classicHID := fullUUID("1124")
	cases := []struct {
		name    string
		present bool
		device  func(*deviceState)
		status  string
		reason  string
	}{
		{
			name:    "a connected controller",
			present: true,
			device:  func(*deviceState) {},
			status:  conditionTrue,
			reason:  reasonLinkUp,
		},
		{
			name:    "a Low Energy remote between presses",
			present: true,
			device: func(d *deviceState) {
				d.Connected, d.AddressType = false, "random"
			},
			status: conditionFalse,
			reason: reasonAsleep,
		},
		{
			name:    "a HID-over-GATT device on a public address",
			present: true,
			device: func(d *deviceState) {
				d.Connected, d.AddressType = false, "public"
				d.UUIDs = []string{hidOverGATT}
			},
			status: conditionFalse,
			reason: reasonAsleep,
		},
		{
			name:    "a classic controller that is switched off",
			present: true,
			device: func(d *deviceState) {
				d.Connected, d.AddressType = false, "public"
				d.UUIDs = []string{classicHID}
			},
			status: conditionFalse,
			reason: reasonNotConnected,
		},
		{
			name:    "a bond bluetoothd holds no object for",
			present: false,
			device:  func(*deviceState) {},
			status:  conditionFalse,
			reason:  reasonNotBonded,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			device := pairedDevice(t, testDevice)
			test.device(&device)

			condition := connectedCondition(nil, device, test.present, testNow)

			if condition.Type != conditionConnected {
				t.Errorf("type = %q", condition.Type)
			}
			if condition.Status != test.status || condition.Reason != test.reason {
				t.Errorf("condition = %+v, want %s/%s", condition, test.status, test.reason)
			}
		})
	}
}

// lastTransitionTime marks when the link state last changed, so a
// reader can measure how long a controller has been asleep. A reason
// that changes under the same status does not move it.
func TestConnectedConditionKeepsTheTimeOfTheLastChange(t *testing.T) {
	device := pairedDevice(t, testDevice)
	device.Connected, device.AddressType = false, "random"
	asleep := connectedCondition(nil, device, true, testNow)

	later := testNow.Add(time.Hour)
	device.AddressType = "public"
	same := connectedCondition([]Condition{asleep}, device, true, later)
	if same.LastTransitionTime != asleep.LastTransitionTime {
		t.Errorf("lastTransitionTime = %q, want the time of the last change %q",
			same.LastTransitionTime, asleep.LastTransitionTime)
	}
	if same.Reason != reasonNotConnected {
		t.Errorf("reason = %q, want the reason to follow the device", same.Reason)
	}

	device.Connected = true
	changed := connectedCondition([]Condition{asleep}, device, true, later)
	if changed.LastTransitionTime != timestamp(later) {
		t.Errorf("lastTransitionTime = %q, want the moment the link came up", changed.LastTransitionTime)
	}
}
