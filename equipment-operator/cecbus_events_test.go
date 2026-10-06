package main

// The Events of the Deployment's CECBus loop: each transition of a
// bus's or a Television's conditions, and each Television that
// discovery creates.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
)

// startEventedBuses is the Deployment's CECBus loop at derivedAt, with
// a recorder.
func startEventedBuses(api *cecAPI, t *testing.T) *cecBusController {
	controller := newCECBusController(api.client)
	controller.now = func() time.Time { return derivedAt }
	controller.recorder = testRecorder(t, api.client)
	return controller
}

// An adapter that refuses its mode posts a Warning for each condition
// it fails. Joined and Scanned fail with one reason and one message,
// so the recorder counts the two on one Event. A pass that finds no
// transition posts nothing more.
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

// Discovery posts the Television it creates on the bus whose TV it
// found, and the next pass posts the new Television's conditions.
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
