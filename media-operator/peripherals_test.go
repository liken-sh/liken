package main

import "testing"

// bonded builds a Peripheral with the given Connected status.
func bonded(name, status string) Peripheral {
	return Peripheral{
		Metadata: ObjectMeta{Name: name},
		Status: PeripheralStatus{
			Conditions: []PeripheralCondition{{Type: peripheralConnected, Status: status}},
		},
	}
}

// A Peripheral that does not exist, and one with no Connected
// condition, has an unknown link: neither connected nor disconnected.
func TestTheLinkComesFromTheConnectedCondition(t *testing.T) {
	cache := newPeripheralCache()
	cache.replace([]Peripheral{
		bonded("aa-bb-cc-dd-ee-ff", conditionTrue),
		bonded("11-22-33-44-55-66", "False"),
		{Metadata: ObjectMeta{Name: "no-conditions"}},
	}, nil)

	cases := []struct {
		name      string
		device    string
		connected bool
		known     bool
	}{
		{name: "a connected device", device: "aa-bb-cc-dd-ee-ff", connected: true, known: true},
		{name: "a device that is away", device: "11-22-33-44-55-66", known: true},
		{name: "a device with no condition yet", device: "no-conditions"},
		{name: "a device the cluster does not hold", device: "77-88-99-aa-bb-cc"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			connected, known := cache.connectedFor(each.device)
			mustMatch(t, known, each.known)
			mustMatch(t, connected, each.connected)
		})
	}
}

func TestTheChargeComesFromTheBatteryBlock(t *testing.T) {
	charged := bonded("aa-bb-cc-dd-ee-ff", conditionTrue)
	charged.Status.Battery = &PeripheralBattery{Percentage: 62}
	cache := newPeripheralCache()
	cache.replace([]Peripheral{charged, bonded("11-22-33-44-55-66", conditionTrue)}, nil)

	level := cache.batteryFor("aa-bb-cc-dd-ee-ff")
	mustMatch(t, level != nil, true)
	mustMatch(t, *level, 62)
	mustMatch(t, cache.batteryFor("11-22-33-44-55-66") == nil, true)
	mustMatch(t, cache.batteryFor("77-88-99-aa-bb-cc") == nil, true)
}

func TestTheCacheReturnsTheAllocatedPeripheralOfAController(t *testing.T) {
	cache := newPeripheralCache()
	cache.replace(nil, map[string]string{
		controllerKey("house", "sofa"): "aa-bb-cc-dd-ee-ff",
	})

	mustMatch(t, cache.peripheralFor(controllerKey("house", "sofa")), "aa-bb-cc-dd-ee-ff")
	mustMatch(t, cache.peripheralFor(controllerKey("house", "chair")), "")
}

func TestTheCacheKeepsOnlyWhatTheLastPassRead(t *testing.T) {
	cache := newPeripheralCache()
	cache.replace([]Peripheral{bonded("aa-bb-cc-dd-ee-ff", conditionTrue)},
		map[string]string{controllerKey("house", "sofa"): "aa-bb-cc-dd-ee-ff"})

	cache.replace(nil, nil)

	_, known := cache.connectedFor("aa-bb-cc-dd-ee-ff")
	mustMatch(t, known, false)
	mustMatch(t, cache.peripheralFor(controllerKey("house", "sofa")), "")
}
