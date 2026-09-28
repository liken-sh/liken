package main

// These tests cover the Peripheral desk: the link it reads off a
// Peripheral's conditions, the charge it reads off the battery block, and
// the answers it gives for a controller the cluster no longer holds.

import "testing"

// bonded builds one Peripheral with the link its Connected condition
// states, so a case states a controller in one line.
func bonded(name, status string) Peripheral {
	return Peripheral{
		Metadata: ObjectMeta{Name: name},
		Status: PeripheralStatus{
			Conditions: []PeripheralCondition{{Type: peripheralConnected, Status: status}},
		},
	}
}

// The desk answers the link off the Connected condition. A Peripheral the
// cluster does not hold, and one that carries no Connected condition, is
// neither connected nor disconnected, so the desk holds no answer.
func TestTheDeskReadsTheLinkOffTheConnectedCondition(t *testing.T) {
	desk := newPeripheralDesk()
	desk.hold([]Peripheral{
		bonded("aa-bb-cc-dd-ee-ff", conditionTrue),
		bonded("11-22-33-44-55-66", "False"),
		{Metadata: ObjectMeta{Name: "no-conditions"}},
	}, nil)

	cases := []struct {
		name      string
		device    string
		connected bool
		held      bool
	}{
		{name: "a connected device", device: "aa-bb-cc-dd-ee-ff", connected: true, held: true},
		{name: "a device that is away", device: "11-22-33-44-55-66", held: true},
		{name: "a device with no condition yet", device: "no-conditions"},
		{name: "a device the cluster does not hold", device: "77-88-99-aa-bb-cc"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			connected, held := desk.connectedFor(each.device)
			mustMatch(t, held, each.held)
			mustMatch(t, connected, each.connected)
		})
	}
}

// The charge comes off the battery block. A device that reports no level
// carries no block, and the desk answers nil, so the status it appears in
// carries no battery key.
func TestTheDeskReadsTheChargeOffTheBatteryBlock(t *testing.T) {
	charged := bonded("aa-bb-cc-dd-ee-ff", conditionTrue)
	charged.Status.Battery = &PeripheralBattery{Percentage: 62}
	desk := newPeripheralDesk()
	desk.hold([]Peripheral{charged, bonded("11-22-33-44-55-66", conditionTrue)}, nil)

	level := desk.batteryFor("aa-bb-cc-dd-ee-ff")
	mustMatch(t, level != nil, true)
	mustMatch(t, *level, 62)
	mustMatch(t, desk.batteryFor("11-22-33-44-55-66") == nil, true)
	mustMatch(t, desk.batteryFor("77-88-99-aa-bb-cc") == nil, true)
}

// The desk names the Peripheral one controller's claim allocated. A
// controller the pass resolved none for names none.
func TestTheDeskNamesThePeripheralAControllerHolds(t *testing.T) {
	desk := newPeripheralDesk()
	desk.hold(nil, map[string]string{
		controllerKey("house", "sofa"): "aa-bb-cc-dd-ee-ff",
	})

	mustMatch(t, desk.peripheralFor(controllerKey("house", "sofa")), "aa-bb-cc-dd-ee-ff")
	mustMatch(t, desk.peripheralFor(controllerKey("house", "chair")), "")
}

// Each pass replaces the whole desk, so a Peripheral the cluster no longer
// holds and a controller whose claim lost its allocation leave nothing
// behind.
func TestTheDeskHoldsOnlyWhatTheLastPassRead(t *testing.T) {
	desk := newPeripheralDesk()
	desk.hold([]Peripheral{bonded("aa-bb-cc-dd-ee-ff", conditionTrue)},
		map[string]string{controllerKey("house", "sofa"): "aa-bb-cc-dd-ee-ff"})

	desk.hold(nil, nil)

	_, held := desk.connectedFor("aa-bb-cc-dd-ee-ff")
	mustMatch(t, held, false)
	mustMatch(t, desk.peripheralFor(controllerKey("house", "sofa")), "")
}
