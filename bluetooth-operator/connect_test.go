package main

// Which bonded devices the operator pages, how long it waits after a
// failure, and that the call never runs on the reconcile loop.

import (
	"errors"
	"testing"
	"time"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// testSpeaker is the A2DP speaker these tests page. It stays paired,
// trusted, and disconnected after a power cycle, because it never
// pages the radio itself.
const testSpeaker = "E3:28:E9:23:21:6F"

func speakerAddress(t *testing.T) bonds.Address {
	t.Helper()
	return testAddress(t, testSpeaker)
}

// bondedSpeaker is the speaker as bluetoothd reports it while it is
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

// seenSpeaker is the same hardware with no bond, which is what a
// peripheral window reports before anybody approves it.
func seenSpeaker(t *testing.T) deviceState {
	t.Helper()
	device := bondedSpeaker(t)
	device.Paired = false
	return device
}

// bondedController is a game controller, asleep until its own button
// pages the radio. A page at it never succeeds and only wastes radio
// time.
func bondedController(t *testing.T) deviceState {
	t.Helper()
	device := pairedDevice(t, testDevice)
	device.Connected = false
	device.UUIDs = []string{fullUUID("1124")}
	return device
}

// connectPass is the pass state the connector reads and writes: the
// bonds a teardown holds, and how soon the loop must run again.
func connectPass() inventoryPass {
	return inventoryPass{unpairing: map[bonds.Address]bool{}}
}

// testConnector reads its clock through a pointer the test moves, so
// a wait runs out without any waiting.
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
				t.Fatal(err)
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

// A teardown disconnects the device and removes the bond, so a page
// in the middle of it would work against the unpair.
func TestConnectSkipsADeletingPeripheral(t *testing.T) {
	fixture := newAPIFixture()
	radio := testRadio(t, connectedSpeaker(t))
	inventory := testInventory(t, fixture, radio)
	inventory.reconcile()
	deleteSpeakerPeripheral(t, fixture)
	// The teardown's first step disconnects the speaker, which is the
	// state a page would answer.
	inventory.reconcile()

	radio.calls = nil
	inventory.reconcile()
	inventory.connects.attempts.Wait()

	if radio.called("Connect") {
		t.Fatalf("a device under teardown was paged: %v", radio.calls)
	}
}

// deleteSpeakerPeripheral marks the Peripheral the way a kubectl delete
// does: the API server stamps a deletionTimestamp and keeps the
// object, because the operator's finalizer is on it.
func deleteSpeakerPeripheral(t *testing.T, fixture *apiFixture) {
	t.Helper()
	path := peripheralPath(speakerAddress(t).Key())
	peripheral := read[Peripheral](t, fixture, path)
	peripheral.Metadata.DeletionTimestamp = timestamp(testNow)
	fixture.put(t, path, peripheral)
}

// A speaker that is switched off fails every page, and each page
// holds the radio for the page timeout. A pass inside the wait asks
// for the wake that ends the wait instead of calling again.
func TestAFailedConnectWaitsBeforeTheNextOne(t *testing.T) {
	radio := testRadio(t, bondedSpeaker(t))
	radio.connectErr = errors.New("org.bluez.Error.Failed: Host is down")
	now := testNow
	connector := testConnector(radio, &now)
	snapshot, err := radio.Snapshot()
	if err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
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

// The wait is per device, so a speaker that answers starts from the
// first delay again the next time it is switched off.
func TestASuccessResetsTheConnectWait(t *testing.T) {
	radio := testRadio(t, bondedSpeaker(t))
	radio.connectErr = errors.New("org.bluez.Error.Failed: Host is down")
	now := testNow
	connector := testConnector(radio, &now)
	failed, err := radio.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	pass := connectPass()
	connector.reconcile(failed, &pass)
	connector.attempts.Wait()

	// The speaker is switched on now, and the wait has run out.
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

// bluetoothd reports a connection whatever made it, and a connected
// device costs this operator nothing per pass.
func TestAConnectionResetsTheConnectWait(t *testing.T) {
	radio := testRadio(t, bondedSpeaker(t))
	radio.connectErr = errors.New("org.bluez.Error.Failed: Host is down")
	now := testNow
	connector := testConnector(radio, &now)
	failed, err := radio.Snapshot()
	if err != nil {
		t.Fatal(err)
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
		t.Fatalf("a connected device asked for another pass in %s", steady.again)
	}
}

// Device1.Connect returns only when the link is up or the page
// failed, and the loop that calls it also publishes the slice and
// writes the bonds, so the loop must not wait for it.
func TestTheReconcilePassDoesNotWaitForAConnect(t *testing.T) {
	radio := testRadio(t, bondedSpeaker(t))
	radio.connectHolds = make(chan struct{})
	now := testNow
	connector := testConnector(radio, &now)
	snapshot, err := radio.Snapshot()
	if err != nil {
		t.Fatal(err)
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
		t.Fatal("the pass waited for the connect to finish")
	}

	// One call at a time for a device: a pass that runs while the
	// first is in flight starts nothing.
	second := connectPass()
	connector.reconcile(snapshot, &second)
	close(radio.connectHolds)
	connector.attempts.Wait()

	if got := radio.counted("Connect"); got != 1 {
		t.Fatalf("Connect ran %d times, want 1: %v", got, radio.calls)
	}
}

// The goroutine that finishes a page is not the loop, so the state it
// changed reaches the next pass only because it asks for one.
func TestAFinishedConnectWakesTheLoop(t *testing.T) {
	radio := testRadio(t, bondedSpeaker(t))
	now := testNow
	connector := testConnector(radio, &now)
	woke := make(chan struct{}, 1)
	connector.wake = func() { woke <- struct{}{} }
	snapshot, err := radio.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	pass := connectPass()
	connector.reconcile(snapshot, &pass)

	select {
	case <-woke:
	case <-time.After(5 * time.Second):
		t.Fatal("the finished connect asked for no pass")
	}
}
