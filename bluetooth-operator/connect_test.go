package main

// Which bonded devices the operator connects, how long it waits after a
// failed attempt, and that the call does not block the reconcile loop.

import (
	"errors"
	"testing"
	"time"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
)

// testSpeaker is an A2DP speaker that never connects on its own.
const testSpeaker = "E3:28:E9:23:21:6F"

func speakerAddress(t *testing.T) bonds.Address {
	t.Helper()
	return testAddress(t, testSpeaker)
}

// bondedSpeaker is the speaker as bluetoothd reports it when it is
// powered on and not connected.
func bondedSpeaker(t *testing.T) deviceState {
	t.Helper()
	return deviceState{
		Address: speakerAddress(t),
		Name:    "B06+",
		Alias:   "studio-pa",
		Paired:  true,
		Trusted: true,
		UUIDs:   []string{fullUUID("110b"), fullUUID("110c")},
	}
}

func connectedSpeaker(t *testing.T) deviceState {
	t.Helper()
	device := bondedSpeaker(t)
	device.Connected = true
	return device
}

func untrustedSpeaker(t *testing.T) deviceState {
	t.Helper()
	device := bondedSpeaker(t)
	device.Trusted = false
	return device
}

// seenSpeaker is the same speaker with no bond, as bluetoothd reports
// it during a pairing window before anyone approves it.
func seenSpeaker(t *testing.T) deviceState {
	t.Helper()
	device := bondedSpeaker(t)
	device.Paired = false
	return device
}

// bondedController is a disconnected game controller.
func bondedController(t *testing.T) deviceState {
	t.Helper()
	device := pairedDevice(t, testDevice)
	device.Connected = false
	device.UUIDs = []string{fullUUID("1124")}
	return device
}

// connectPass returns an empty pass: no unpairs in progress and no
// request to run again.
func connectPass() inventoryPass {
	return inventoryPass{unpairing: map[bonds.Address]bool{}}
}

// testConnector returns a connector whose clock is *now, so a test can
// move time forward by assigning to it.
func testConnector(radio *fakeRadio, now *time.Time) *connector {
	return newConnector(radio, func() time.Time { return *now })
}

func TestConnectPagesOnlyABondedTrustedAudioSink(t *testing.T) {
	cases := []struct {
		name   string
		device deviceState
		want   int
	}{
		{"a bonded, trusted, disconnected speaker", bondedSpeaker(t), 1},
		{"a speaker that already holds a connection", connectedSpeaker(t), 0},
		{"a speaker nobody trusted", untrustedSpeaker(t), 0},
		{"a speaker the radio has only observed", seenSpeaker(t), 0},
		{"a controller, which pages the radio itself", bondedController(t), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			radio := testRadio(t, c.device)
			now := testNow
			connector := testConnector(radio, &now)
			snapshot, err := radio.Snapshot()
			if err != nil {
				t.Fatalf("reading the radio snapshot: %v", err)
			}

			pass := connectPass()
			connector.reconcile(snapshot, &pass)
			connector.attempts.Wait()

			if got := radio.counted("Connect"); got != c.want {
				t.Fatalf("Connect ran %d times, want %d: %v", got, c.want, radio.calls)
			}
		})
	}
}

// An unpair disconnects the device and removes the bond, so connecting
// the device during it would undo the unpair.
func TestConnectSkipsADeletingPeripheral(t *testing.T) {
	fixture := newAPIFixture()
	radio := testRadio(t, connectedSpeaker(t))
	inventory := testInventory(t, fixture, radio)
	inventory.reconcile()
	deleteSpeakerPeripheral(t, fixture)
	// The unpair disconnects the speaker first, so the speaker is now
	// a disconnected, bonded, trusted sink that the connector would
	// otherwise connect.
	inventory.reconcile()

	radio.calls = nil
	inventory.reconcile()
	inventory.connects.attempts.Wait()

	if radio.called("Connect") {
		t.Fatalf("Connect ran for a device under teardown: %v, want no call", radio.calls)
	}
}

// deleteSpeakerPeripheral deletes the speaker's Peripheral as kubectl
// does. The operator's finalizer keeps the object, and the API server
// sets its deletionTimestamp.
func deleteSpeakerPeripheral(t *testing.T, fixture *apiFixture) {
	t.Helper()
	path := peripheralPath(speakerAddress(t).Key())
	peripheral := read[Peripheral](t, fixture, path)
	peripheral.Metadata.DeletionTimestamp = timestamp(testNow)
	fixture.put(t, path, peripheral)
}

// A pass during the backoff does not call Connect, and asks to run again
// when the backoff ends.
func TestAFailedConnectWaitsBeforeTheNextOne(t *testing.T) {
	radio := testRadio(t, bondedSpeaker(t))
	radio.connectErr = errors.New("org.bluez.Error.Failed: Host is down")
	now := testNow
	connector := testConnector(radio, &now)
	snapshot, err := radio.Snapshot()
	if err != nil {
		t.Fatalf("reading the radio snapshot: %v", err)
	}

	first := connectPass()
	connector.reconcile(snapshot, &first)
	connector.attempts.Wait()

	second := connectPass()
	connector.reconcile(snapshot, &second)
	connector.attempts.Wait()

	if got := radio.counted("Connect"); got != 1 {
		t.Fatalf("Connect ran %d times inside the wait, want 1: %v", got, radio.calls)
	}
	if second.again != firstConnectRetry {
		t.Fatalf("the pass asks to run again in %s, want %s", second.again, firstConnectRetry)
	}
}

func TestTheConnectWaitDoublesToACeiling(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 10 * time.Second},
		{2, 20 * time.Second},
		{3, 40 * time.Second},
		{4, 80 * time.Second},
		{5, 120 * time.Second},
		{6, 120 * time.Second},
	}
	radio := testRadio(t, bondedSpeaker(t))
	radio.connectErr = errors.New("org.bluez.Error.Failed: Host is down")
	now := testNow
	connector := testConnector(radio, &now)
	snapshot, err := radio.Snapshot()
	if err != nil {
		t.Fatalf("reading the radio snapshot: %v", err)
	}

	for _, c := range cases {
		pass := connectPass()
		connector.reconcile(snapshot, &pass)
		connector.attempts.Wait()

		if got := connector.backoff[speakerAddress(t)]; got != c.want {
			t.Fatalf("after failure %d the wait is %s, want %s", c.attempt, got, c.want)
		}
		now = now.Add(c.want)
	}
}

// The backoff is per device. A speaker that connects starts again from
// firstConnectRetry the next time it fails.
func TestASuccessResetsTheConnectWait(t *testing.T) {
	radio := testRadio(t, bondedSpeaker(t))
	radio.connectErr = errors.New("org.bluez.Error.Failed: Host is down")
	now := testNow
	connector := testConnector(radio, &now)
	failed, err := radio.Snapshot()
	if err != nil {
		t.Fatalf("reading the radio snapshot: %v", err)
	}
	pass := connectPass()
	connector.reconcile(failed, &pass)
	connector.attempts.Wait()

	// The speaker is switched on and the backoff has expired.
	radio.connectErr = nil
	now = now.Add(firstConnectRetry)
	answered := connectPass()
	connector.reconcile(failed, &answered)
	connector.attempts.Wait()

	if got := connector.backoff[speakerAddress(t)]; got != 0 {
		t.Fatalf("the wait after a success is %s, want none", got)
	}
	if got := radio.counted("Connect"); got != 2 {
		t.Fatalf("Connect ran %d times, want 2: %v", got, radio.calls)
	}
}

// A device that bluetoothd reports as connected, by this operator or by
// itself, clears its backoff and asks for no further pass.
func TestAConnectionResetsTheConnectWait(t *testing.T) {
	radio := testRadio(t, bondedSpeaker(t))
	radio.connectErr = errors.New("org.bluez.Error.Failed: Host is down")
	now := testNow
	connector := testConnector(radio, &now)
	failed, err := radio.Snapshot()
	if err != nil {
		t.Fatalf("reading the radio snapshot: %v", err)
	}
	pass := connectPass()
	connector.reconcile(failed, &pass)
	connector.attempts.Wait()

	connected := radioSnapshot{Adapter: failed.Adapter, Devices: []deviceState{connectedSpeaker(t)}}
	steady := connectPass()
	connector.reconcile(connected, &steady)
	connector.attempts.Wait()

	if got := connector.backoff[speakerAddress(t)]; got != 0 {
		t.Fatalf("the wait after a connection is %s, want none", got)
	}
	if steady.again != 0 {
		t.Fatalf("a connected device asked for another pass in %s, want none", steady.again)
	}
}

// The reconcile pass returns while a Connect call is still in flight.
func TestTheReconcilePassDoesNotWaitForAConnect(t *testing.T) {
	radio := testRadio(t, bondedSpeaker(t))
	radio.connectBlocks = make(chan struct{})
	now := testNow
	connector := testConnector(radio, &now)
	snapshot, err := radio.Snapshot()
	if err != nil {
		t.Fatalf("reading the radio snapshot: %v", err)
	}

	returned := make(chan struct{})
	pass := connectPass()
	go func() {
		defer close(returned)
		connector.reconcile(snapshot, &pass)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("the pass did not return within 5 seconds, want it to return while Connect is in flight")
	}

	// A second pass while the first call is in flight starts no
	// second call.
	second := connectPass()
	connector.reconcile(snapshot, &second)
	close(radio.connectBlocks)
	connector.attempts.Wait()

	if got := radio.counted("Connect"); got != 1 {
		t.Fatalf("Connect ran %d times, want 1: %v", got, radio.calls)
	}
}

// The goroutine that makes the call asks the reconcile loop for a pass
// when the call returns, so the loop reads the new state.
func TestAFinishedConnectWakesTheLoop(t *testing.T) {
	radio := testRadio(t, bondedSpeaker(t))
	now := testNow
	connector := testConnector(radio, &now)
	woke := make(chan struct{}, 1)
	connector.wake = func() { woke <- struct{}{} }
	snapshot, err := radio.Snapshot()
	if err != nil {
		t.Fatalf("reading the radio snapshot: %v", err)
	}

	pass := connectPass()
	connector.reconcile(snapshot, &pass)

	select {
	case <-woke:
	case <-time.After(5 * time.Second):
		t.Fatal("no pass was requested within 5 seconds of Connect returning, want one")
	}
}
