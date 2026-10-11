package main

// The Peripheral facts under bluetooth_: the link, the battery, and
// whether a claim holds the controller. peripheral_test.go covers the
// object itself; this file covers what writePeripheralStatus reports
// to metrics.go beside it.

import (
	"strings"
	"testing"
)

// The gauge follows the Connected condition it is read beside.
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

// Adopting a bond that is already disconnected is not a disconnect:
// this operator never watched a link go down, it only learned that
// one is down.
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

// A link that drops after a pass reported it connected is one
// disconnect, and a pass that reports the same drop again is not a
// second one: the counter measures the edge, not the level.
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
		t.Errorf("bluetooth_disconnects_total = %v (found: %v), want 1 for one observed drop held over two passes", count, found)
	}
}

// The counter carries the reason the Connected condition took, so a
// remote that sleeps counts apart from a link that the radio closed.
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

// Most controllers report no battery at all, and the gauge carries no
// series for one, rather than a zero that would read as an observed
// empty battery.
func TestPeripheralBatteryGaugeIsAbsentWithNoSource(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))

	inventory.reconcile()

	if _, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_peripheral_battery_percent", map[string]string{"peripheral": "a0-ab-51-33-b7-12"}); found {
		t.Error("bluetooth_peripheral_battery_percent carries a series for a device with no battery source")
	}
}

// A reported charge of zero is a real, observed empty battery, and it
// reads on the gauge like any other level.
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

// The gauge follows status.battery off the object, so a level from the
// kernel and a level that later goes silent both reach it.
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
		t.Error("bluetooth_peripheral_battery_percent still carries a series once the source went silent")
	}
}

// The claimed gauge answers from the same CDI read the unpair
// teardown already makes: one while a prepared claim holds the
// controller, zero once the kubelet unprepares it.
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
		t.Fatal(err)
	}
	inventory.reconcile()
	if claimed, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_peripheral_claimed", map[string]string{"peripheral": "a0-ab-51-33-b7-12"}); !found || claimed != 0 {
		t.Errorf("bluetooth_peripheral_claimed = %v (found: %v), want 0 once the claim released", claimed, found)
	}
}

// The bonded gauge follows status.bond.bonded, which is what BlueZ
// reports about the stored link key.
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

// An unpair takes the Peripheral off every series, the disconnects of
// every reason and the wake presses among them.
func TestForgettingAPeripheralClearsEverySeries(t *testing.T) {
	readings := newMetrics()
	readings.countDisconnect("c2-11-22-33-44-55", reasonAsleep)
	readings.countDisconnect("c2-11-22-33-44-55", reasonClosedByRadio)
	readings.countWakePress("c2-11-22-33-44-55")

	readings.forgetPeripheral("c2-11-22-33-44-55")

	page := scrape(t, readings)
	if strings.Contains(page, "c2-11-22-33-44-55") {
		t.Errorf("the registry still carries the Peripheral after it was forgotten:\n%s", page)
	}
}
