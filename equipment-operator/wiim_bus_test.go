package main

// The WiiM bus path: a settings message reaches the device and is
// written back into spec.wiim.settings, and a command message reaches
// the device as a one-shot.

import (
	"testing"
	"time"
)

func TestAWiimBusSettingReachesTheDeviceAndTheSpec(t *testing.T) {
	amp := startFakeWiim(t)
	_, unit := waitingWiim(t, amp)
	api, apiClient := startDiscoveryAPI(t)
	unit.client = apiClient

	unit.handleSettings([]byte(`{"setting":"device.led","value":false}`))

	if !contains(amp.sent(), "LED_SWITCH_SET:0") {
		t.Fatalf("the device did not get the setting: %v", amp.sent())
	}
	mustDeepEqual(t, api.appliedNames(), []string{"studio"})
}

func TestAWiimBusCommandReachesTheDevice(t *testing.T) {
	amp := startFakeWiim(t)
	_, unit := waitingWiim(t, amp)
	api, apiClient := startDiscoveryAPI(t)
	unit.client = apiClient

	unit.handleCommand([]byte(`{"command":"preset.recall","args":{"number":3}}`))

	if !contains(amp.sent(), "MCUKeyShortClick:3") {
		t.Fatalf("the device did not get the command: %v", amp.sent())
	}
	// A one-shot command is not a setting, so nothing is written back to
	// the spec.
	mustDeepEqual(t, api.appliedNames(), []string(nil))
}

// A settings message the device refuses writes nothing back to the spec.
func TestARefusedWiimSettingIsNotRecorded(t *testing.T) {
	amp := startFakeWiim(t)
	_, unit := waitingWiim(t, amp)
	api, apiClient := startDiscoveryAPI(t)
	unit.client = apiClient

	unit.handleSettings([]byte(`{"setting":"device.nonsense","value":true}`))
	time.Sleep(20 * time.Millisecond)

	mustDeepEqual(t, api.appliedNames(), []string(nil))
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
