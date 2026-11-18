package main

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
)

// testRemote is a Low Energy remote, HID over GATT through bluetoothd
// and /dev/uhid. It sleeps between presses and wakes on the next one.
const testRemote = "FE:5C:63:A3:FF:2E"

func remoteAddress(t *testing.T) bonds.Address {
	t.Helper()
	return testAddress(t, testRemote)
}

// connectedRemote is the remote as bluetoothd reports it while its link
// is up.
func connectedRemote(t *testing.T) deviceState {
	t.Helper()
	return deviceState{
		Address:     remoteAddress(t),
		Name:        "T6-Remote",
		Alias:       "T6-Remote",
		AddressType: "random",
		Paired:      true,
		Bonded:      true,
		Trusted:     true,
		Connected:   true,
		UUIDs:       []string{fullUUID("1812")},
	}
}

func sleepingRemote(t *testing.T) deviceState {
	t.Helper()
	device := connectedRemote(t)
	device.Connected = false
	return device
}

// remoteRelays returns relays with the remote's virtual devices,
// restored from the snapshot in its bond's Secret, as for a controller
// that has had input nodes before.
func remoteRelays(t *testing.T) *relays {
	t.Helper()
	set := newRelays(newFakeKernel())
	stored := fmt.Sprintf(`{"version":%d,"nodes":[{"name":"T6-Remote Keyboard"},{"name":"T6-Remote Mouse"}]}`, snapshotVersion)
	set.restore(normalizeMAC(testRemote), []byte(stored))
	t.Cleanup(func() { stopAll(set) })
	return set
}

// remoteHID is the HID device the kernel registers for the remote once
// bluetoothd attaches its input profile.
func remoteHID() []hidDevice {
	return []hidDevice{{
		MAC:     normalizeMAC(testRemote),
		DevPath: "/devices/virtual/misc/uhid/0005:1915:EEEE.0007",
		Nodes:   []string{"/dev/input/event7", "/dev/input/event8"},
	}}
}

// testRecovery builds an inputRecovery whose clock reads the time that
// now points to, so a test advances time by assigning to it.
func testRecovery(radio *fakeRadio, set *relays, now *time.Time) *inputRecovery {
	return newInputRecovery(radio, set, func() time.Time { return *now })
}

// recoveryPass runs one pass at the clock's current time and waits for
// any reconnect the pass starts.
func recoveryPass(t *testing.T, recovery *inputRecovery, radio *fakeRadio, kernel []hidDevice, unpairing map[bonds.Address]bool) inventoryPass {
	t.Helper()
	snapshot, err := radio.Snapshot()
	if err != nil {
		t.Fatalf("reading the radio snapshot: %v", err)
	}
	pass := inventoryPass{unpairing: unpairing}
	recovery.reconcile(snapshot, kernel, &pass)
	recovery.attempts.Wait()
	return pass
}

// A controller that is connected, has delivered input before, and has
// no kernel HID device after the grace period is reconnected through
// bluetoothd. No other controller is.
func TestRecoveryReconnectsOnlyAConnectedControllerWithNoHIDDevice(t *testing.T) {
	reconnect := []string{"Disconnect " + remoteAddress(t).Key(), "Connect " + remoteAddress(t).Key()}
	cases := []struct {
		name      string
		device    deviceState
		set       func(*testing.T) *relays
		kernel    []hidDevice
		unpairing map[bonds.Address]bool
		elapsed   time.Duration
		want      []string
	}{
		{
			name:    "connected with no HID device past the grace",
			device:  connectedRemote(t),
			set:     remoteRelays,
			elapsed: recoveryGrace,
			want:    reconnect,
		},
		{
			name:    "connected with no HID device inside the grace",
			device:  connectedRemote(t),
			set:     remoteRelays,
			elapsed: recoveryGrace - time.Second,
		},
		{
			name:    "connected with its HID device",
			device:  connectedRemote(t),
			set:     remoteRelays,
			kernel:  remoteHID(),
			elapsed: recoveryGrace,
		},
		{
			name:    "asleep",
			device:  sleepingRemote(t),
			set:     remoteRelays,
			elapsed: recoveryGrace,
		},
		{
			name:    "a device that has never delivered input, such as a speaker",
			device:  connectedRemote(t),
			set:     func(*testing.T) *relays { return newRelays(newFakeKernel()) },
			elapsed: recoveryGrace,
		},
		{
			name:      "a device under teardown",
			device:    connectedRemote(t),
			set:       remoteRelays,
			unpairing: map[bonds.Address]bool{remoteAddress(t): true},
			elapsed:   recoveryGrace,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			radio := testRadio(t, c.device)
			now := testNow
			recovery := testRecovery(radio, c.set(t), &now)

			recoveryPass(t, recovery, radio, c.kernel, c.unpairing)
			now = now.Add(c.elapsed)
			recoveryPass(t, recovery, radio, c.kernel, c.unpairing)

			if !slices.Equal(radio.calls, c.want) {
				t.Fatalf("calls = %v, want %v", radio.calls, c.want)
			}
		})
	}
}

// Nothing signals the end of the grace period, so a pass inside it asks
// the loop to run again when it ends.
func TestRecoveryRequestsAnotherPassWhenTheGraceEnds(t *testing.T) {
	radio := testRadio(t, connectedRemote(t))
	now := testNow
	recovery := testRecovery(radio, remoteRelays(t), &now)

	recoveryPass(t, recovery, radio, nil, nil)
	now = now.Add(5 * time.Second)
	pass := recoveryPass(t, recovery, radio, nil, nil)

	if pass.again != recoveryGrace-5*time.Second {
		t.Fatalf("the pass asks to run again in %s, want %s", pass.again, recoveryGrace-5*time.Second)
	}
}

// A reconnect that brings no HID device back is not repeated at once.
// The wait doubles after each failed reconnect up to a ceiling, so a
// controller that never recovers is reconnected every 15 minutes and
// not on every pass.
func TestTheRecoveryWaitDoublesToACeiling(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, time.Minute},
		{2, 2 * time.Minute},
		{3, 4 * time.Minute},
		{4, 8 * time.Minute},
		{5, 15 * time.Minute},
		{6, 15 * time.Minute},
	}
	radio := testRadio(t, connectedRemote(t))
	now := testNow
	recovery := testRecovery(radio, remoteRelays(t), &now)
	recoveryPass(t, recovery, radio, nil, nil)
	now = now.Add(recoveryGrace)

	for _, c := range cases {
		recoveryPass(t, recovery, radio, nil, nil)
		if got := radio.counted("Disconnect"); got != c.attempt {
			t.Fatalf("after %d waits, Disconnect ran %d times, want %d: %v", c.attempt-1, got, c.attempt, radio.calls)
		}
		early := recoveryPass(t, recovery, radio, nil, nil)
		if early.again != c.want {
			t.Fatalf("after reconnect %d the pass asks to run again in %s, want %s", c.attempt, early.again, c.want)
		}
		now = now.Add(c.want)
	}
}

// The wait is kept while the reconnect itself disconnects the link,
// because a pass can see the device disconnected between the two calls.
// If that cleared the wait, a controller that never recovers would be
// reconnected every recoveryGrace.
func TestTheRecoveryWaitSurvivesTheReconnect(t *testing.T) {
	radio := testRadio(t, connectedRemote(t))
	now := testNow
	recovery := testRecovery(radio, remoteRelays(t), &now)
	recoveryPass(t, recovery, radio, nil, nil)
	now = now.Add(recoveryGrace)
	recoveryPass(t, recovery, radio, nil, nil)

	radio.update(remoteAddress(t), func(state *deviceState) { state.Connected = false })
	recoveryPass(t, recovery, radio, nil, nil)
	radio.update(remoteAddress(t), func(state *deviceState) { state.Connected = true })
	recoveryPass(t, recovery, radio, nil, nil)
	now = now.Add(recoveryGrace)
	recoveryPass(t, recovery, radio, nil, nil)

	if got := radio.counted("Disconnect"); got != 1 {
		t.Fatalf("Disconnect ran %d times inside the wait, want 1: %v", got, radio.calls)
	}
}

// A HID device that appears clears the wait, so a remote that recovered
// and gets stuck again days later is reconnected after the grace period
// and not after the wait left by an old failure.
func TestAHIDDeviceResetsTheRecoveryWait(t *testing.T) {
	radio := testRadio(t, connectedRemote(t))
	now := testNow
	recovery := testRecovery(radio, remoteRelays(t), &now)
	recoveryPass(t, recovery, radio, nil, nil)
	now = now.Add(recoveryGrace)
	recoveryPass(t, recovery, radio, nil, nil)

	recoveryPass(t, recovery, radio, remoteHID(), nil)
	recoveryPass(t, recovery, radio, nil, nil)
	now = now.Add(recoveryGrace)
	recoveryPass(t, recovery, radio, nil, nil)

	if got := radio.counted("Disconnect"); got != 2 {
		t.Fatalf("Disconnect ran %d times, want 2: %v", got, radio.calls)
	}
}

// The inventory pass runs the recovery against the HID devices in
// sysfs, so a remote whose link came up with no input node is
// reconnected without restarting the operator.
func TestTheInventoryPassReconnectsARemoteWithNoInputNode(t *testing.T) {
	fixture := newAPIFixture()
	radio := testRadio(t, connectedRemote(t))
	cdiTempDir(t)
	sysfsFor(t)
	inventory := newInventory(testClient(t, fixture.handler(t)), radio, remoteRelays(t), "liken-1", "liken-system", newMetrics())
	now := testNow
	inventory.now = func() time.Time { return now }

	inventory.reconcile()
	now = now.Add(recoveryGrace)
	inventory.reconcile()
	inventory.inputs.attempts.Wait()

	want := []string{"Disconnect " + remoteAddress(t).Key(), "Connect " + remoteAddress(t).Key()}
	var got []string
	for _, call := range radio.calls {
		if slices.Contains(want, call) {
			got = append(got, call)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", radio.calls, want)
	}
}
