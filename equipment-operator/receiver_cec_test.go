package main

// The Deployment's status.cec on a Receiver: the audio system of a bus
// as the bus's adapters found it, on the Receiver that is the via of a
// Display on that bus, and the Event of each change of the System
// Audio Mode.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
)

// audioSystemIn is the receiver of the den bus as an adapter reports
// it, in a System Audio Mode.
func audioSystemIn(mode, at string) CECDevice {
	device := receiverDevice
	device.Vendor, device.CECVersion = "0005cd", "1.4"
	device.SystemAudioMode, device.SystemAudioModeAt = mode, at
	return device
}

// wiredRoom is the den bus with the TV and a receiver in a mode, the
// Display on node-1, a Television, and the given Receivers.
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

// receiverCEC answers the stored status.cec of a Receiver.
func receiverCEC(api *cecAPI, name string) *ReceiverCECStatus {
	api.mutex.Lock()
	defer api.mutex.Unlock()
	return api.receivers[name].Status.CEC
}

// cecWritesOf counts the Deployment's status.cec writes.
func cecWritesOf(api *cecAPI) int {
	api.mutex.Lock()
	defer api.mutex.Unlock()
	return api.receiverCECWrites
}

// The Receiver that a Display's picture passes through gets the bus's
// audio system, with each fact as the adapters report it. A Receiver
// that no Display's picture passes through gets none.
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

// A Receiver that stops being on the path loses status.cec, because
// the bus's audio system is no longer known to be it.
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

// A pass that finds the same audio system writes nothing.
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

// Each change of the System Audio Mode posts a Normal Event on the
// Receiver, Off as well as On: the receiver turns the mode off before
// standby, and a person turns it off by picking the TV's speakers, so
// Off is no fault. A mode first learned posts one too.
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
