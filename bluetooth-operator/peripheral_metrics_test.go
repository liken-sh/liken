package main

// These tests cover the bluetooth_peripheral_* metrics and
// bluetooth_disconnects_total, which the reconcile pass writes from
// each Peripheral.

import (
	"strings"
	"testing"
)

// bluetooth_peripheral_connected is 1 while the link is up and 0 after
// it drops.
func TestPeripheralConnectedGaugeFollowsTheLink(t *testing.T) {
	fixture := newAPIFixture()
	radio := testRadio(t, pairedDevice(t, testDevice))
	inventory := testInventory(t, fixture, radio)

	inventory.reconcile()
	if connected, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_peripheral_connected", map[string]string{"peripheral": "a0-ab-51-33-b7-12"}); !found || connected != 1 {
		t.Errorf("bluetooth_peripheral_connected = %v (found: %v), want 1", connected, found)
	}

	radio.update(testAddress(t, testDevice), func(d *deviceState) { d.Connected = false })
	inventory.reconcile()
	if connected, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_peripheral_connected", map[string]string{"peripheral": "a0-ab-51-33-b7-12"}); !found || connected != 0 {
		t.Errorf("bluetooth_peripheral_connected = %v (found: %v), want 0 after the link dropped", connected, found)
	}
}

// A bond that is already disconnected when the operator first sees it
// does not count as a disconnect, because the operator did not see the
// link drop.
func TestFirstObservationNeverCountsAsADisconnect(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Connected = false
	inventory := testInventory(t, fixture, testRadio(t, device))

	inventory.reconcile()

	if count, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_disconnects_total", map[string]string{"peripheral": "a0-ab-51-33-b7-12", "reason": reasonNotConnected}); found && count != 0 {
		t.Errorf("bluetooth_disconnects_total = %v, want none for the first observation", count)
	}
}

// A link that drops after a pass saw it connected counts once. Later
// passes that still see it disconnected do not count again.
func TestDisconnectsCountOnlyTheObservedTransition(t *testing.T) {
	fixture := newAPIFixture()
	radio := testRadio(t, pairedDevice(t, testDevice))
	inventory := testInventory(t, fixture, radio)
	inventory.reconcile()

	radio.update(testAddress(t, testDevice), func(d *deviceState) { d.Connected = false })
	inventory.reconcile()
	inventory.reconcile()

	count, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_disconnects_total", map[string]string{"peripheral": "a0-ab-51-33-b7-12", "reason": reasonNotConnected})
	if !found || count != 1 {
		t.Errorf("bluetooth_disconnects_total = %v (found: %v), want 1 for one observed drop seen over two passes", count, found)
	}
}

// The counter's reason label is the reason of the Connected condition,
// so a sleeping remote and a link that the radio closed are separate
// series.
func TestDisconnectsCountUnderTheConditionsReason(t *testing.T) {
	cases := []struct {
		ended  string
		reason string
	}{
		{blueZReasonTimeout, reasonAsleep},
		{blueZReasonLocal, reasonClosedByRadio},
		{"", reasonNotConnected},
	}
	for _, c := range cases {
		t.Run(c.reason, func(t *testing.T) {
			fixture := newAPIFixture()
			radio := testRadio(t, connectedRemote(t))
			inventory := testInventory(t, fixture, radio)
			inventory.reconcile()

			radio.update(remoteAddress(t), func(d *deviceState) { d.Connected = false })
			inventory.ends.record(remoteAddress(t), c.ended)
			inventory.reconcile()

			count, found := metricValue(t, inventory.metrics.registry,
				"bluetooth_disconnects_total", map[string]string{"peripheral": "fe-5c-63-a3-ff-2e", "reason": c.reason})
			if !found || count != 1 {
				t.Errorf("bluetooth_disconnects_total{reason=%q} = %v (found: %v), want 1", c.reason, count, found)
			}
		})
	}
}

// A device that reports no battery has no series on the gauge. A zero
// would read as an empty battery.
func TestPeripheralBatteryGaugeIsAbsentWithNoSource(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))

	inventory.reconcile()

	if _, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_peripheral_battery_percent", map[string]string{"peripheral": "a0-ab-51-33-b7-12"}); found {
		t.Error("bluetooth_peripheral_battery_percent has a series for a device with no battery source, want none")
	}
}

// A reported charge of 0 percent is a real reading, so the gauge shows 0.
func TestPeripheralBatteryGaugeReportsAnObservedZero(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Battery = &deviceBattery{Percentage: 0, Source: "HID"}
	inventory := testInventory(t, fixture, testRadio(t, device))

	inventory.reconcile()

	level, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_peripheral_battery_percent", map[string]string{"peripheral": "a0-ab-51-33-b7-12"})
	if !found || level != 0 {
		t.Errorf("bluetooth_peripheral_battery_percent = %v (found: %v), want an observed 0", level, found)
	}
}

// The gauge follows status.battery, so its series is removed when the
// kernel stops reporting a battery level.
func TestPeripheralBatteryGaugeClearsWhenTheSourceGoesSilent(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	inventory := testInventory(t, fixture, testRadio(t, device))
	sysfsFor(t, withBattery(dualSense("0001", testDevice, "input/event5"), "40", "Discharging"))
	inventory.reconcile()

	if level, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_peripheral_battery_percent", map[string]string{"peripheral": "a0-ab-51-33-b7-12"}); !found || level != 40 {
		t.Fatalf("bluetooth_peripheral_battery_percent = %v (found: %v), want 40", level, found)
	}

	sysfsFor(t)
	inventory.reconcile()
	if _, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_peripheral_battery_percent", map[string]string{"peripheral": "a0-ab-51-33-b7-12"}); found {
		t.Error("bluetooth_peripheral_battery_percent still has a series after the battery source went silent, want none")
	}
}

// bluetooth_peripheral_claimed is 1 while a prepared claim holds the
// controller, which the operator reads from the CDI spec files, and 0
// once the kubelet unprepares the claim.
func TestPeripheralClaimedGaugeFollowsAPreparedClaim(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))
	const claimUID = "0f1e2d3c-0000-4000-8000-000000000009"
	prepareClaim(t, claimUID, "a0-ab-51-33-b7-12", "/dev/input/event5")

	inventory.reconcile()
	if claimed, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_peripheral_claimed", map[string]string{"peripheral": "a0-ab-51-33-b7-12"}); !found || claimed != 1 {
		t.Errorf("bluetooth_peripheral_claimed = %v (found: %v), want 1", claimed, found)
	}

	if err := removeCDISpec(claimUID); err != nil {
		t.Fatalf("removing the CDI spec: %v", err)
	}
	inventory.reconcile()
	if claimed, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_peripheral_claimed", map[string]string{"peripheral": "a0-ab-51-33-b7-12"}); !found || claimed != 0 {
		t.Errorf("bluetooth_peripheral_claimed = %v (found: %v), want 0 once the claim released", claimed, found)
	}
}

// bluetooth_peripheral_bonded follows status.bond.bonded, which copies
// the Bonded property of the BlueZ device.
func TestPeripheralBondedGaugeFollowsTheStoredKeys(t *testing.T) {
	fixture := newAPIFixture()
	radio := testRadio(t, pairedDevice(t, testDevice))
	inventory := testInventory(t, fixture, radio)

	inventory.reconcile()
	if bonded, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_peripheral_bonded", map[string]string{"peripheral": "a0-ab-51-33-b7-12"}); !found || bonded != 1 {
		t.Errorf("bluetooth_peripheral_bonded = %v (found: %v), want 1", bonded, found)
	}

	radio.update(testAddress(t, testDevice), func(d *deviceState) { d.Bonded = false })
	inventory.reconcile()
	if bonded, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_peripheral_bonded", map[string]string{"peripheral": "a0-ab-51-33-b7-12"}); !found || bonded != 0 {
		t.Errorf("bluetooth_peripheral_bonded = %v (found: %v), want 0 once the keys are gone", bonded, found)
	}
}

// forgetPeripheral removes every series of the Peripheral, including
// its disconnects of each reason and its wake presses.
func TestForgettingAPeripheralClearsEverySeries(t *testing.T) {
	readings := newMetrics()
	readings.countDisconnect("c2-11-22-33-44-55", reasonAsleep)
	readings.countDisconnect("c2-11-22-33-44-55", reasonClosedByRadio)
	readings.countWakePress("c2-11-22-33-44-55")

	readings.forgetPeripheral("c2-11-22-33-44-55")

	page := scrape(t, readings)
	if strings.Contains(page, "c2-11-22-33-44-55") {
		t.Errorf("the registry still has the Peripheral after it was forgotten, want none:\n%s", page)
	}
}
