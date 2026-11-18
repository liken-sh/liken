package main

// The tests of a Receiver's status.cec and of the Event for each change
// of the System Audio Mode.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
)

// audioSystemIn returns the den bus's receiver in the given System
// Audio Mode.
func audioSystemIn(mode, at string) CECDevice {
	device := receiverDevice
	device.Vendor, device.CECVersion = "0005cd", "1.4"
	device.SystemAudioMode, device.SystemAudioModeAt = mode, at
	return device
}

// wiredRoom creates the den bus with a TV and a receiver in the given
// mode, a Display on node-1, a Television, and the given Receivers.
func wiredRoom(t *testing.T, mode string, receivers ...Receiver) *cecAPI {
	t.Helper()
	api := startCECAPI(t)
	api.putBus(*busWith(CECControl, []string{"node-1"}, scannedEntry("node-1", 4, tvDevice, audioSystemIn(mode, "2026-09-26T11:59:00Z"))))
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	for _, receiver := range receivers {
		api.putReceiver(receiver)
	}
	api.putTelevision(Television{Metadata: ObjectMeta{Name: "lounge"}, Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: "den"}}})
	return api
}

func receiverCEC(api *cecAPI, name string) *ReceiverCECStatus {
	api.mutex.Lock()
	defer api.mutex.Unlock()
	return api.receivers[name].Status.CEC
}

func cecWritesOf(api *cecAPI) int {
	api.mutex.Lock()
	defer api.mutex.Unlock()
	return api.receiverCECWrites
}

func TestTheReceiverOnThePathGetsTheAudioSystem(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := wiredRoom(t, "On", wiredReceiver("den", "node-1", "acm-0001-receiver"), wiredReceiver("kitchen", "node-2", "acm-0001-receiver"))

		passes(t, api, 2)

		mustDeepEqual(t, receiverCEC(api, "den"), &ReceiverCECStatus{
			Bus: "den", LogicalAddress: 5, PhysicalAddress: "1.0.0.0", OSDName: "AVR", Vendor: "0005cd", CECVersion: "1.4",
			SystemAudioMode: "On", SystemAudioModeAt: "2026-09-26T11:59:00Z",
		})
		mustDeepEqual(t, receiverCEC(api, "kitchen"), (*ReceiverCECStatus)(nil))
	})
}

func TestAReceiverOffThePathLosesTheAudioSystem(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := wiredRoom(t, "On", wiredReceiver("den", "node-1", "acm-0001-receiver"))
		passes(t, api, 1)

		api.putReceiver(wiredReceiver("den", "node-2", "acm-0001-receiver"))
		passes(t, api, 1)

		mustDeepEqual(t, receiverCEC(api, "den"), (*ReceiverCECStatus)(nil))
	})
}

func TestAnUnchangedAudioSystemIsNotWrittenAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := wiredRoom(t, "On", wiredReceiver("den", "node-1", "acm-0001-receiver"))
		passes(t, api, 1)
		first := cecWritesOf(api)

		passes(t, api, 2)

		mustMatch(t, first, 1)
		mustMatch(t, cecWritesOf(api), first)
	})
}

// The Event for Off is Normal, not Warning, because the receiver turns
// the mode off before it goes to standby, and a person turns it off by
// choosing the TV's speakers. The first value the operator reads posts
// an Event too.
func TestEachModeChangePostsANormalEvent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := wiredRoom(t, "On", wiredReceiver("den", "node-1", "acm-0001-receiver"))
		controller := startEventedBuses(api, t)
		mustSucceed(t, controller.pass())
		mustSucceed(t, controller.pass())

		api.putBus(*busWith(CECControl, []string{"node-1"}, scannedEntry("node-1", 4, tvDevice, audioSystemIn("Off", "2026-09-26T11:59:30Z"))))
		mustSucceed(t, controller.pass())
		mustSucceed(t, controller.pass())
		time.Sleep(time.Second)

		mustDeepEqual(t, postedAbout(api.events, "Receiver", "den"), []posted{
			{events.TypeNormal, reasonSystemAudioModeOn, 1},
			{events.TypeNormal, reasonSystemAudioModeOff, 1},
		})
	})
}
