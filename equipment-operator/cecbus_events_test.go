package main

// Tests of the Events that the Deployment's CECBus loop posts.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
)

// startEventedBuses returns the Deployment's CECBus loop with its clock
// fixed at derivedAt and an Event recorder. The test runs each pass.
func startEventedBuses(api *cecAPI, t *testing.T) *cecBusController {
	controller := newCECBusController(api.client, testNamespace)
	controller.now = func() time.Time { return derivedAt }
	controller.recorder = testRecorder(t, api.client)
	return controller
}

// Joined and Scanned fail with the same reason and message, so the
// recorder counts both on one Event. The second pass has no transition
// and posts nothing.
func TestARefusedAdapterPostsAWarningOnItsBus(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.putBus(*busWith(CECControl, []string{"node-1"}, CECAdapterStatus{Machine: "node-1", State: AdapterRefused, Message: "CEC_S_MODE: device or resource busy"}))
		controller := startEventedBuses(api, t)

		mustSucceed(t, controller.pass())
		mustSucceed(t, controller.pass())

		mustDeepEqual(t, postedAbout(api.events, "CECBus", "den"), []posted{
			{events.TypeWarning, reasonNoAddress, 1},
			{events.TypeWarning, reasonRefused, 2},
			{events.TypeNormal, reasonOneAdapter, 1},
		})
	})
}

// When discovery creates a Television, it posts an Event on the bus,
// and the next pass posts the Television's conditions.
func TestDiscoveryPostsTheTelevisionItCreates(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.putBus(*busWith(CECControl, []string{"node-1"}, scannedEntry("node-1", 4, tvDevice)))
		controller := startEventedBuses(api, t)

		mustSucceed(t, controller.pass())
		mustSucceed(t, controller.pass())

		busEvents := postedAbout(api.events, "CECBus", "den")
		mustMatch(t, busEvents[len(busEvents)-1], posted{events.TypeNormal, reasonTelevisionCreated, 1})
		mustDeepEqual(t, postedAbout(api.events, "Television", "den"), []posted{
			{events.TypeNormal, reasonAnswers, 1},
			{events.TypeNormal, reasonInCharge, 1},
		})
	})
}
