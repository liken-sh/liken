package main

// Each test runs in a synctest bubble because the recorder writes from
// its own goroutine, and synctest.Wait returns once that goroutine has
// written every queued Event.

import (
	"errors"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

// testNode is the Node the tests' operator runs on.
var testNode = events.ObjectReference{APIVersion: "v1", Kind: "Node", Name: "liken-1", UID: "uid-liken-1"}

// eventInventory builds an inventory like testInventory does, on the
// in-memory connections of apiservertest, with a recorder that writes
// to a fake events collection. The caller runs in a synctest bubble.
func eventInventory(t *testing.T, fixture *apiFixture, radio *fakeRadio) (*inventory, *eventstest.Events) {
	t.Helper()
	cdiTempDir(t)
	sysfsFor(t)
	recorded := &eventstest.Events{}
	server := apiservertest.Start(t, recorded.Around(fixture.handler(t)))
	client := apiclient.New(apiservertest.Host, server.Client(), "")
	i := newInventory(client, radio, relaysFor(t), "liken-1", "liken-system", newMetrics())
	i.now = func() time.Time { return testNow }
	i.recorder = events.New(t.Context(), client, component, events.Options{Instance: "liken-1", Log: io.Discard})
	i.node = testNode
	return i, recorded
}

// posted is one Event as a test compares it: its type, its reason, and
// its message.
type posted struct {
	Type, Reason, Message string
}

// postedAbout returns the Events about one object, after the recorder
// has written every queued Event.
func postedAbout(recorded *eventstest.Events, kind, name string) []posted {
	synctest.Wait()
	var out []posted
	for _, event := range recorded.About(kind, name) {
		out = append(out, posted{event.Type, event.Reason, event.Message})
	}
	return out
}

func assertPosted(t *testing.T, got []posted, want ...posted) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("Events = %+v, want %+v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("Event %d = %+v, want %+v", index, got[index], want[index])
		}
	}
}

// One pass that opens a window and pairs the approved device posts the
// window opening and the pairing on the request, and the pairing on the
// new Peripheral.
func TestAPairingPostsTheWindowAndThePairing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture()
		fixture.put(t, testRequestPath(), openRequest(testDevice))
		radio := testRadio(t, seenDevice(t, testDevice, "DualSense Wireless Controller"))
		inventory, recorded := eventInventory(t, fixture, radio)

		inventory.reconcile()

		assertPosted(t, postedAbout(recorded, pairingRequestKind, testRequestName),
			posted{"Normal", reasonPairingWindowOpened, "The radio 14-b4-57-91-2f-c8 is discoverable and pairable until 2026-08-17T17:33:00Z"},
			posted{"Normal", reasonPaired, "Paired with A0:AB:51:33:B7:12 and created Peripheral a0-ab-51-33-b7-12"},
		)
		assertPosted(t, postedAbout(recorded, peripheralKind, "a0-ab-51-33-b7-12"),
			posted{"Normal", reasonPaired, "Paired with A0:AB:51:33:B7:12 through PairingRequest liken-system/new-gamepad"},
			posted{"Normal", reasonNotConnected, connectedMessages[reasonNotConnected]},
		)
	})
}

func TestAWindowThatClosesUnapprovedPostsItsExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture()
		request := openRequest("")
		request.Status = PairingRequestStatus{
			Phase:          phaseOpen,
			WindowClosesAt: timestamp(testNow.Add(-time.Second)),
		}
		fixture.put(t, testRequestPath(), request)
		inventory, recorded := eventInventory(t, fixture, testRadio(t))

		inventory.reconcile()

		assertPosted(t, postedAbout(recorded, pairingRequestKind, testRequestName),
			posted{"Normal", reasonPairingWindowExpired, "The pairing window closed at 2026-08-17T17:29:59Z with no device paired"},
		)
	})
}

// The window retries a refused pairing on each pass, and the refusal is
// posted once, when the request's message first reports it.
func TestARefusedPairingPostsOneWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture()
		fixture.put(t, testRequestPath(), openRequest(testDevice))
		radio := testRadio(t, seenDevice(t, testDevice, "DualSense Wireless Controller"))
		radio.pairErr = errors.New("org.bluez.Error.AuthenticationFailed")
		inventory, recorded := eventInventory(t, fixture, radio)

		inventory.reconcile()
		inventory.reconcile()

		assertPosted(t, postedAbout(recorded, pairingRequestKind, testRequestName),
			posted{"Normal", reasonPairingWindowOpened, "The radio 14-b4-57-91-2f-c8 is discoverable and pairable until 2026-08-17T17:33:00Z"},
			posted{"Warning", reasonPairingRefused, "bluetoothd did not pair with A0:AB:51:33:B7:12: org.bluez.Error.AuthenticationFailed"},
		)
		for _, event := range recorded.About(pairingRequestKind, testRequestName) {
			if event.Count != 1 {
				t.Errorf("%s posted %d times, want once", event.Reason, event.Count)
			}
		}
	})
}

// A bond removed outside the operator posts one Warning. Adopting an
// existing bond into a Peripheral posts nothing, because no pairing
// happened.
func TestABondThatLeftBluetoothdPostsBondLost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture()
		inventory, recorded := eventInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))
		inventory.reconcile()

		inventory.radio.(*fakeRadio).snapshot.Devices = nil
		inventory.reconcile()
		inventory.reconcile()

		assertPosted(t, postedAbout(recorded, peripheralKind, "a0-ab-51-33-b7-12"),
			posted{"Normal", reasonLinkUp, connectedMessages[reasonLinkUp]},
			posted{"Normal", reasonNotBonded, connectedMessages[reasonNotBonded]},
			posted{"Warning", reasonBondLost, "bluetoothd has no bond with A0:AB:51:33:B7:12. Delete the Peripheral to unpair it, or pair the device again"},
		)
	})
}

// Each transition of the Connected condition posts an Event with its
// reason: a remote that sleeps and wakes, and a link whose keys did not
// match, which needs a person to pair again. A pass that finds the same
// state again posts nothing.
func TestEachTransitionOfTheLinkPostsItsReason(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		radio := testRadio(t, connectedRemote(t))
		inventory, recorded := eventInventory(t, newAPIFixture(), radio)
		inventory.reconcile()
		// The recorder counts a repeat within 10 minutes on the Event it
		// posted, so each change of the link comes later than that.
		link := func(connected bool, ended string) {
			time.Sleep(11 * time.Minute)
			radio.update(remoteAddress(t), func(d *deviceState) { d.Connected = connected })
			inventory.ends.record(remoteAddress(t), ended)
			inventory.reconcile()
			inventory.reconcile()
		}

		link(false, blueZReasonTimeout)
		link(true, "")
		link(false, blueZReasonAuthentication)

		assertPosted(t, postedAbout(recorded, peripheralKind, "fe-5c-63-a3-ff-2e"),
			posted{"Normal", reasonLinkUp, connectedMessages[reasonLinkUp]},
			posted{"Normal", reasonAsleep, connectedMessages[reasonAsleep]},
			posted{"Normal", reasonLinkUp, connectedMessages[reasonLinkUp]},
			posted{"Warning", reasonAuthenticationFailed, connectedMessages[reasonAuthenticationFailed]},
		)
	})
}

// The radio's arrival and departure are posted on the Node, because the
// Adapter may already be gone when the radio is lost.
func TestTheRadioPostsItsClaimAndItsLossOnTheNode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := newAPIFixture()
		radio := testRadio(t)
		radio.err = ErrNoAdapter
		inventory, recorded := eventInventory(t, fixture, radio)

		// bluetoothd has not reported a radio yet. That is normal at pod
		// start, so it posts no loss.
		inventory.reconcile()
		radio.err = nil
		inventory.reconcile()
		inventory.reconcile()
		radio.err = ErrNoAdapter
		inventory.reconcile()

		assertPosted(t, postedAbout(recorded, "Node", "liken-1"),
			posted{"Normal", reasonRadioClaimed, "bluetoothd reports the radio at 14:B4:57:91:2F:C8"},
			posted{"Warning", reasonRadioLost, "bluetoothd no longer reports the radio at 14:B4:57:91:2F:C8"},
		)
	})
}

// A relay the kernel refused posts one Warning on the controller's
// Peripheral although each pass retries it. The Warning is posted again
// only after a relay was created in between.
func TestARefusedRelayPostsOneWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorded := &eventstest.Events{}
		server := apiservertest.Start(t, recorded.Around(newAPIFixture().handler(t)))
		client := apiclient.New(apiservertest.Host, server.Client(), "")
		kernel := newFakeKernel()
		kernel.register("/dev/input/event5", "Wireless Controller")
		kernel.createErr = errors.New("open /dev/uinput: permission denied")
		inputRelays := newRelays(kernel)
		inputRelays.recorder = events.New(t.Context(), client, component, events.Options{Log: io.Discard})
		t.Cleanup(func() { inputRelays.stop(testMAC) })

		inputRelays.ensure(testMAC, []string{"/dev/input/event5"})
		inputRelays.ensure(testMAC, []string{"/dev/input/event5"})

		assertPosted(t, postedAbout(recorded, peripheralKind, "a0-ab-51-33-b7-12"),
			posted{"Warning", reasonInputRelayFailed, "The operator cannot create the virtual input device of A0:AB:51:33:B7:12: open /dev/uinput: permission denied"},
		)
	})
}
