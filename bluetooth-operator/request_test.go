package main

// These tests cover the PairingRequest's state machine: the window
// opens and reports what the radio sees, an approval pairs exactly the
// device a person named, an empty spec.device pairs nothing at all,
// the window expires unapproved, and a finished request is collected
// after its TTL.

import (
	"errors"
	"testing"
	"time"
)

const testRequestName = "new-gamepad"

func testRequestPath() string { return pairingRequestPath("liken-system", testRequestName) }

// openRequest is a request a person has just created, with no status
// on it yet.
func openRequest(device string) *PairingRequest {
	return &PairingRequest{
		APIVersion: pairingAPI,
		Kind:       pairingRequestKind,
		Metadata:   ObjectMeta{Name: testRequestName, Namespace: "liken-system"},
		Spec: PairingRequestSpec{
			Adapter:       testAdapterName,
			WindowSeconds: 180,
			Device:        device,
		},
	}
}

func TestRequestOpensAWindowAndReportsWhatTheRadioSees(t *testing.T) {
	fixture := newAPIFixture()
	fixture.put(t, testRequestPath(), openRequest(""))
	radio := testRadio(t, seenDevice(t, testDevice, "DualSense Wireless Controller"))
	inventory := testInventory(t, fixture, radio)

	pass := inventory.reconcile()

	if !radio.called("OpenWindow 3m0s") {
		t.Fatalf("the window never reached the radio: %v", radio.calls)
	}
	request := read[PairingRequest](t, fixture, testRequestPath())
	if request.Status.Phase != phaseOpen {
		t.Errorf("phase = %q", request.Status.Phase)
	}
	if request.Status.WindowClosesAt != timestamp(testNow.Add(3*time.Minute)) {
		t.Errorf("windowClosesAt = %q", request.Status.WindowClosesAt)
	}
	if len(request.Status.Seen) != 1 {
		t.Fatalf("seen = %+v, want the one device the radio can see", request.Status.Seen)
	}
	if request.Status.Seen[0].Address != testDevice ||
		request.Status.Seen[0].Name != "DualSense Wireless Controller" ||
		request.Status.Seen[0].FirstSeen != timestamp(testNow) {
		t.Errorf("seen[0] = %+v", request.Status.Seen[0])
	}
	// An open window means a person is waiting, so the loop runs again
	// sooner than its backstop tick.
	if pass.again == 0 {
		t.Error("an open window asked for no follow-up pass")
	}
}

// Pairing whatever responds first is the one behavior that can bond a
// stranger's device, so an unapproved window pairs nothing.
func TestRequestNeverPairsWithoutAnApproval(t *testing.T) {
	fixture := newAPIFixture()
	fixture.put(t, testRequestPath(), openRequest(""))
	radio := testRadio(t, seenDevice(t, testDevice, "DualSense Wireless Controller"))
	inventory := testInventory(t, fixture, radio)

	inventory.reconcile()

	if radio.called("Pair") {
		t.Fatalf("an empty spec.device paired something: %v", radio.calls)
	}
	if _, found := fixture.objects[testPairingPath()]; found {
		t.Fatal("an empty spec.device created a Pairing")
	}
}

func TestRequestPairsTheDeviceAPersonApproved(t *testing.T) {
	fixture := newAPIFixture()
	fixture.put(t, testRequestPath(), openRequest(testDevice))
	radio := testRadio(t, seenDevice(t, testDevice, "DualSense Wireless Controller"))
	inventory := testInventory(t, fixture, radio)

	pass := inventory.reconcile()

	if !radio.called("Pair a0-ab-51-33-b7-12") {
		t.Fatalf("the approved device was not paired: %v", radio.calls)
	}
	// Trusting it is what lets the controller reconnect on its own,
	// with no agent registered.
	if !radio.called("SetDeviceTrusted a0-ab-51-33-b7-12 true") {
		t.Errorf("the device was not trusted: %v", radio.calls)
	}
	request := read[PairingRequest](t, fixture, testRequestPath())
	if request.Status.Phase != phasePaired || request.Status.Pairing != "a0-ab-51-33-b7-12" {
		t.Errorf("status = %+v", request.Status)
	}
	if request.Status.FinishedAt != timestamp(testNow) {
		t.Errorf("status.finishedAt = %q", request.Status.FinishedAt)
	}
	// The Pairing records the request that produced it, so that record
	// outlasts the request's collection.
	pairing := read[Pairing](t, fixture, testPairingPath())
	if pairing.Status.Request != "liken-system/"+testRequestName {
		t.Errorf("the Pairing does not name the request: %+v", pairing.Status)
	}
	if pairing.Spec.Trusted == nil || !*pairing.Spec.Trusted {
		t.Errorf("spec.trusted = %v, want true", pairing.Spec.Trusted)
	}
	// The bond's Secret follows on the same pass, because the Pairing
	// that owns it exists now.
	if _, owned := pass.owners[testAddress(t, testDevice)]; !owned {
		t.Errorf("owners = %+v, want the new bond", pass.owners)
	}
	// The window is not left open after the bond is made.
	if !radio.called("CloseWindow") {
		t.Errorf("the window stayed open after the pairing: %v", radio.calls)
	}
}

// The controller is not responding to the scan yet, which is the
// ordinary state before somebody holds its buttons. The window stays
// open.
func TestRequestWaitsForAnApprovedDeviceToAnswer(t *testing.T) {
	fixture := newAPIFixture()
	fixture.put(t, testRequestPath(), openRequest(testDevice))
	radio := testRadio(t)
	inventory := testInventory(t, fixture, radio)

	inventory.reconcile()

	if radio.called("Pair") {
		t.Fatalf("a device the radio cannot see was paired: %v", radio.calls)
	}
	request := read[PairingRequest](t, fixture, testRequestPath())
	if request.Status.Phase != phaseOpen || request.Status.Message == "" {
		t.Errorf("status = %+v", request.Status)
	}
}

// A pairing bluetoothd refused is reported and left for the window to
// try again, because the controller's own pairing mode may still be
// running.
func TestRequestReportsAPairingTheRadioRefused(t *testing.T) {
	fixture := newAPIFixture()
	fixture.put(t, testRequestPath(), openRequest(testDevice))
	radio := testRadio(t, seenDevice(t, testDevice, "DualSense Wireless Controller"))
	radio.pairErr = errors.New("org.bluez.Error.AuthenticationFailed")
	inventory := testInventory(t, fixture, radio)

	inventory.reconcile()

	request := read[PairingRequest](t, fixture, testRequestPath())
	if request.Status.Phase != phaseOpen {
		t.Errorf("phase = %q, want the window still open", request.Status.Phase)
	}
	if request.Status.Message == "" {
		t.Error("nothing in the status reports why the pairing did not happen")
	}
	if _, found := fixture.objects[testPairingPath()]; found {
		t.Error("a failed pairing recorded a Pairing")
	}
}

func TestRequestExpiresWhenItsWindowCloses(t *testing.T) {
	fixture := newAPIFixture()
	request := openRequest("")
	request.Status = PairingRequestStatus{
		Phase:          phaseOpen,
		WindowClosesAt: timestamp(testNow.Add(-time.Second)),
	}
	fixture.put(t, testRequestPath(), request)
	radio := testRadio(t)
	radio.snapshot.Adapter.Discoverable = true
	inventory := testInventory(t, fixture, radio)

	inventory.reconcile()

	expired := read[PairingRequest](t, fixture, testRequestPath())
	if expired.Status.Phase != phaseExpired || expired.Status.FinishedAt != timestamp(testNow) {
		t.Errorf("status = %+v", expired.Status)
	}
	// The radio goes back to not discoverable and not pairable, which
	// is what everything outside a window depends on.
	if !radio.called("CloseWindow") {
		t.Fatalf("the radio was left in a window: %v", radio.calls)
	}
}

// A request names one adapter. The operator that holds that radio
// serves it, and no other operator opens a window on hardware a
// person did not name.
func TestRequestForAnotherRadioIsLeftAlone(t *testing.T) {
	fixture := newAPIFixture()
	request := openRequest("")
	request.Spec.Adapter = "04-4a-69-66-92-27"
	fixture.put(t, testRequestPath(), request)
	radio := testRadio(t)
	inventory := testInventory(t, fixture, radio)

	inventory.reconcile()

	if radio.called("OpenWindow") {
		t.Fatalf("another radio's request opened a window here: %v", radio.calls)
	}
	untouched := read[PairingRequest](t, fixture, testRequestPath())
	if untouched.Status.Phase != "" {
		t.Errorf("status = %+v", untouched.Status)
	}
}

func TestFinishedRequestIsCollectedAfterItsTTL(t *testing.T) {
	fixture := newAPIFixture()
	request := openRequest(testDevice)
	request.Status = PairingRequestStatus{
		Phase:      phasePaired,
		Pairing:    "a0-ab-51-33-b7-12",
		FinishedAt: timestamp(testNow.Add(-25 * time.Hour)),
	}
	fixture.put(t, testRequestPath(), request)
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))

	inventory.reconcile()

	if _, found := fixture.objects[testRequestPath()]; found {
		t.Fatal("a finished request outlived its TTL")
	}
}

func TestFinishedRequestStaysUntilItsTTL(t *testing.T) {
	fixture := newAPIFixture()
	request := openRequest(testDevice)
	request.Status = PairingRequestStatus{
		Phase:      phaseExpired,
		FinishedAt: timestamp(testNow.Add(-time.Hour)),
	}
	fixture.put(t, testRequestPath(), request)
	inventory := testInventory(t, fixture, testRadio(t))

	inventory.reconcile()

	if _, found := fixture.objects[testRequestPath()]; !found {
		t.Fatal("a request was collected before its TTL")
	}
}

// A TTL needs a start. A finished request with no time on it gets one
// written, because a pass that counted from itself would never collect
// the request and the watcher would wake the loop for it forever.
func TestFinishedRequestWithNoTimeGetsOne(t *testing.T) {
	fixture := newAPIFixture()
	request := openRequest("")
	request.Status = PairingRequestStatus{Phase: phaseExpired}
	fixture.put(t, testRequestPath(), request)
	inventory := testInventory(t, fixture, testRadio(t))

	inventory.reconcile()

	stamped := read[PairingRequest](t, fixture, testRequestPath())
	if stamped.Status.FinishedAt != timestamp(testNow) {
		t.Fatalf("status.finishedAt = %q", stamped.Status.FinishedAt)
	}
}

// The seen list is written from radio observations, so a busy room
// must not grow the object without limit.
func TestSeenListIsCappedAndItsNamesAreCut(t *testing.T) {
	snapshot := radioSnapshot{}
	long := ""
	for len(long) < 100 {
		long += "controller-"
	}
	for index := 0; index < 20; index++ {
		snapshot.Devices = append(snapshot.Devices, deviceState{
			Address: bondsAddress(t, index),
			Alias:   long,
		})
	}

	seen := seenDevices(nil, snapshot, testNow)

	if len(seen) != maxSeenDevices {
		t.Fatalf("seen holds %d devices, want %d", len(seen), maxSeenDevices)
	}
	for _, device := range seen {
		if len(device.Name) > maxSeenNameBytes {
			t.Errorf("name is %d bytes: %q", len(device.Name), device.Name)
		}
	}
}

// A device keeps the firstSeen it was given, because that value
// records which of two controllers started responding first.
func TestSeenListKeepsTheFirstSighting(t *testing.T) {
	earlier := timestamp(testNow.Add(-time.Minute))
	seen := []SeenDevice{{Address: testDevice, Name: "DualSense Wireless Controller", FirstSeen: earlier}}
	snapshot := radioSnapshot{Devices: []deviceState{seenDevice(t, testDevice, "DualSense Wireless Controller")}}

	merged := seenDevices(seen, snapshot, testNow)

	if len(merged) != 1 || merged[0].FirstSeen != earlier {
		t.Fatalf("seen = %+v, want the first sighting kept", merged)
	}
}

// A device the radio already holds a bond with has a Pairing of its
// own, and the list exists to name the devices that do not.
func TestSeenListLeavesOutTheDevicesAlreadyPaired(t *testing.T) {
	snapshot := radioSnapshot{Devices: []deviceState{pairedDevice(t, testDevice)}}

	if seen := seenDevices(nil, snapshot, testNow); len(seen) != 0 {
		t.Fatalf("seen = %+v, want nothing", seen)
	}
}

// The watcher wakes the loop for a request that is unfinished and for
// one whose collection is due, and for nothing else. On an idle
// cluster a poll must not trigger a pass.
func TestRequestsNeedAPass(t *testing.T) {
	cases := []struct {
		name    string
		request PairingRequest
		want    bool
	}{
		{
			name:    "a request nobody has run yet",
			request: PairingRequest{},
			want:    true,
		},
		{
			name:    "an open window",
			request: PairingRequest{Status: PairingRequestStatus{Phase: phaseOpen}},
			want:    true,
		},
		{
			name: "a finished request inside its TTL",
			request: PairingRequest{Status: PairingRequestStatus{
				Phase:      phasePaired,
				FinishedAt: timestamp(testNow.Add(-time.Hour)),
			}},
			want: false,
		},
		{
			name: "a finished request past its TTL",
			request: PairingRequest{Status: PairingRequestStatus{
				Phase:      phaseExpired,
				FinishedAt: timestamp(testNow.Add(-25 * time.Hour)),
			}},
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := requestsNeedAPass([]PairingRequest{c.request}, testNow); got != c.want {
				t.Fatalf("requestsNeedAPass = %t, want %t", got, c.want)
			}
		})
	}
}

// bondsAddress builds distinct addresses for a test that needs many of
// them.
func bondsAddress(t *testing.T, index int) (address [6]byte) {
	t.Helper()
	address[5] = byte(index)
	return address
}
