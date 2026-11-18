package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const testRequestName = "new-gamepad"

func testRequestPath() string { return pairingRequestPath("liken-system", testRequestName) }

// openRequest returns a new request with no status.
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
		t.Fatalf("the radio calls are %v, want OpenWindow 3m0s", radio.calls)
	}
	request := read[PairingRequest](t, fixture, testRequestPath())
	if request.Status.Phase != phaseOpen {
		t.Errorf("status.phase is %q, want %q", request.Status.Phase, phaseOpen)
	}
	if request.Status.WindowClosesAt != timestamp(testNow.Add(3*time.Minute)) {
		t.Errorf("status.windowClosesAt is %q, want 3 minutes after the pass", request.Status.WindowClosesAt)
	}
	if len(request.Status.Seen) != 1 {
		t.Fatalf("seen = %+v, want the one device the radio can see", request.Status.Seen)
	}
	if request.Status.Seen[0].Address != testDevice ||
		request.Status.Seen[0].Name != "DualSense Wireless Controller" ||
		request.Status.Seen[0].FirstSeen != timestamp(testNow) {
		t.Errorf("status.seen[0] is %+v, want the device with its name and first sighting", request.Status.Seen[0])
	}
	// A person is waiting on the window, so the next pass must come
	// soon.
	if pass.again == 0 {
		t.Error("an open window asked for no follow-up pass")
	}
}

func TestARefusedWindowSchedulesAnotherPass(t *testing.T) {
	fixture := newAPIFixture()
	fixture.put(t, testRequestPath(), openRequest(""))
	radio := testRadio(t)
	radio.windowErr = errors.New("org.bluez.Error.NotReady: Resource Not Ready")
	inventory := testInventory(t, fixture, radio)

	pass := inventory.reconcile()

	request := read[PairingRequest](t, fixture, testRequestPath())
	if !strings.Contains(request.Status.Message, "Resource Not Ready") {
		t.Errorf("message = %q, want the radio's own refusal", request.Status.Message)
	}
	if pass.again == 0 {
		t.Error("a refused window asked for no follow-up pass")
	}
}

// Pairing whichever device answers first could bond a stranger's
// device, so a window with no approval pairs nothing.
func TestRequestNeverPairsWithoutAnApproval(t *testing.T) {
	fixture := newAPIFixture()
	fixture.put(t, testRequestPath(), openRequest(""))
	radio := testRadio(t, seenDevice(t, testDevice, "DualSense Wireless Controller"))
	inventory := testInventory(t, fixture, radio)

	inventory.reconcile()

	if radio.called("Pair") {
		t.Fatalf("an empty spec.device paired something: %v", radio.calls)
	}
	if _, found := fixture.objects[testPeripheralPath()]; found {
		t.Fatal("an empty spec.device created a Peripheral")
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
	// A trusted controller can reconnect with no agent registered.
	if !radio.called("SetDeviceTrusted a0-ab-51-33-b7-12 true") {
		t.Errorf("the radio calls are %v, want SetDeviceTrusted for the device", radio.calls)
	}
	request := read[PairingRequest](t, fixture, testRequestPath())
	if request.Status.Phase != phasePaired || request.Status.Peripheral != "a0-ab-51-33-b7-12" {
		t.Errorf("status = %+v", request.Status)
	}
	if request.Status.FinishedAt != timestamp(testNow) {
		t.Errorf("status.finishedAt is %q, want the time of the pairing", request.Status.FinishedAt)
	}
	peripheral := read[Peripheral](t, fixture, testPeripheralPath())
	if peripheral.Status.Bond.Request != "liken-system/"+testRequestName {
		t.Errorf("the Peripheral does not name the request: %+v", peripheral.Status)
	}
	if peripheral.Spec.Trusted == nil || !*peripheral.Spec.Trusted {
		t.Errorf("spec.trusted = %v, want true", peripheral.Spec.Trusted)
	}
	// pass.owners lets the bond store write the bond's Secret in this
	// same pass.
	if _, owned := pass.owners[testAddress(t, testDevice)]; !owned {
		t.Errorf("owners = %+v, want the new bond", pass.owners)
	}
	if !radio.called("CloseWindow") {
		t.Errorf("the window stayed open after the pairing: %v", radio.calls)
	}
}

// A controller that is not in pairing mode yet is missing from the
// scan, and the window stays open for it.
func TestRequestWaitsForAnApprovedDeviceToAppearInTheScan(t *testing.T) {
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

// The window stays open after a refused pairing, because the
// controller may still be in pairing mode.
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
	if _, found := fixture.objects[testPeripheralPath()]; found {
		t.Error("a failed pairing recorded a Peripheral")
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
		t.Errorf("status is %+v, want phase Expired and finishedAt set", expired.Status)
	}
	if !radio.called("CloseWindow") {
		t.Fatalf("the radio was left in a window: %v", radio.calls)
	}
}

// No operator opens a window on a radio that the request does not
// name.
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
		t.Errorf("status is %+v, want a request for another radio left untouched", untouched.Status)
	}
}

func TestFinishedRequestIsCollectedAfterItsTTL(t *testing.T) {
	fixture := newAPIFixture()
	request := openRequest(testDevice)
	request.Status = PairingRequestStatus{
		Phase:      phasePaired,
		Peripheral: "a0-ab-51-33-b7-12",
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

// Without status.finishedAt, each pass would count the TTL from its
// own time and never delete the request.
func TestFinishedRequestWithNoTimeGetsOne(t *testing.T) {
	fixture := newAPIFixture()
	request := openRequest("")
	request.Status = PairingRequestStatus{Phase: phaseExpired}
	fixture.put(t, testRequestPath(), request)
	inventory := testInventory(t, fixture, testRadio(t))

	inventory.reconcile()

	stamped := read[PairingRequest](t, fixture, testRequestPath())
	if stamped.Status.FinishedAt != timestamp(testNow) {
		t.Fatalf("status.finishedAt is %q, want the time of the pass", stamped.Status.FinishedAt)
	}
}

// A busy room must not grow the request without limit.
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

	seen, truncated := seenDevices(nil, snapshot, testNow)

	if len(seen) != maxSeenDevices {
		t.Fatalf("seen holds %d devices, want %d", len(seen), maxSeenDevices)
	}
	if !truncated {
		t.Error("seenDevices reported no cut for a room of 20 devices")
	}
	for _, device := range seen {
		if len(device.Name) > maxSeenNameBytes {
			t.Errorf("a name is %d bytes, want at most maxSeenNameBytes: %q", len(device.Name), device.Name)
		}
	}
}

func TestSeenListKeepsTheFirstSighting(t *testing.T) {
	earlier := timestamp(testNow.Add(-time.Minute))
	seen := []SeenDevice{{Address: testDevice, Name: "DualSense Wireless Controller", FirstSeen: earlier}}
	snapshot := radioSnapshot{Devices: []deviceState{seenDevice(t, testDevice, "DualSense Wireless Controller")}}

	merged, _ := seenDevices(seen, snapshot, testNow)

	if len(merged) != 1 || merged[0].FirstSeen != earlier {
		t.Fatalf("seen = %+v, want the first sighting kept", merged)
	}
}

func TestSeenListLeavesOutTheDevicesAlreadyPaired(t *testing.T) {
	snapshot := radioSnapshot{Devices: []deviceState{pairedDevice(t, testDevice)}}

	if seen, _ := seenDevices(nil, snapshot, testNow); len(seen) != 0 {
		t.Fatalf("seen = %+v, want nothing", seen)
	}
}

func bondsAddress(t *testing.T, index int) (address [6]byte) {
	t.Helper()
	address[5] = byte(index)
	return address
}

func TestPairAttemptsCountUnderTheirResult(t *testing.T) {
	cases := []struct {
		name    string
		pairErr error
		result  string
	}{
		{name: "bluetoothd completed the bond", result: resultPaired},
		{name: "bluetoothd refused the pairing", pairErr: errors.New("org.bluez.Error.AuthenticationFailed"), result: resultRefused},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newAPIFixture()
			fixture.put(t, testRequestPath(), openRequest(testDevice))
			radio := testRadio(t, seenDevice(t, testDevice, "DualSense Wireless Controller"))
			radio.pairErr = c.pairErr
			inventory := testInventory(t, fixture, radio)

			inventory.reconcile()

			count, found := metricValue(t, inventory.metrics.registry,
				"bluetooth_pair_attempts_total", map[string]string{"result": c.result})
			if !found || count != 1 {
				t.Fatalf("bluetooth_pair_attempts_total{result=%q} = %v (found: %v), want 1", c.result, count, found)
			}
		})
	}
}

// crowdedRadio returns a radio that sees the given number of unpaired
// devices.
func crowdedRadio(t *testing.T, devices int) *fakeRadio {
	t.Helper()
	radio := testRadio(t)
	for index := range devices {
		radio.snapshot.Devices = append(radio.snapshot.Devices, deviceState{
			Address: bondsAddress(t, index),
			Name:    "controller",
		})
	}
	return radio
}

func TestTheRequestReportsACutSeenList(t *testing.T) {
	cases := []struct {
		name    string
		devices int
		want    bool
	}{
		{name: "a list with space left", devices: 3, want: false},
		{name: "a room larger than the cap", devices: maxSeenDevices + 4, want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newAPIFixture()
			fixture.put(t, testRequestPath(), openRequest(""))
			inventory := testInventory(t, fixture, crowdedRadio(t, c.devices))

			inventory.reconcile()

			request := read[PairingRequest](t, fixture, testRequestPath())
			if request.Status.SeenTruncated != c.want {
				t.Fatalf("status.seenTruncated = %t, want %t for %d devices", request.Status.SeenTruncated, c.want, c.devices)
			}
		})
	}
}

// A person who reads the request after the window closes can still
// see that the list was cut.
func TestACutSeenListStaysSetOnAnExpiredRequest(t *testing.T) {
	fixture := newAPIFixture()
	fixture.put(t, testRequestPath(), openRequest(""))
	inventory := testInventory(t, fixture, crowdedRadio(t, maxSeenDevices+4))
	inventory.reconcile()

	inventory.now = func() time.Time { return testNow.Add(4 * time.Minute) }
	inventory.reconcile()

	request := read[PairingRequest](t, fixture, testRequestPath())
	if request.Status.Phase != phaseExpired {
		t.Fatalf("phase = %q, want the window closed", request.Status.Phase)
	}
	if !request.Status.SeenTruncated {
		t.Fatalf("status.seenTruncated = false once the window closed, want the mark kept")
	}
}
