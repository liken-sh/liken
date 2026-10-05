package main

// Which connected controllers the operator reconnects because the
// kernel holds no HID device for them, how long it waits first, and how
// long it waits between two reconnects that did not help.

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// testRemote is a Low Energy remote, HID over GATT through bluetoothd
// and /dev/uhid. It sleeps between presses and wakes on the next one.
const testRemote = "FE:5C:63:A3:FF:2E"

func remoteAddress(t *testing.T) bonds.Address {
	t.Helper()
	return testAddress(t, testRemote)
}

// connectedRemote is the remote as bluetoothd reports it while its
// link is up.
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

// remoteRelays holds the remote's two virtual devices, restored from
// the snapshot its bond's Secret stores. A controller has them once it
// has connected with its input nodes at least once.
func remoteRelays(t *testing.T) *relays {
	t.Helper()
	held := newRelays(newFakeKernel())
	stored := fmt.Sprintf(`{"version":%d,"nodes":[{"name":"T6-Remote Keyboard"},{"name":"T6-Remote Mouse"}]}`, snapshotVersion)
	held.restore(normalizeMAC(testRemote), []byte(stored))
	t.Cleanup(func() { stopAll(held) })
	return held
}

// remoteHID is the HID device the kernel registers for the remote when
// bluetoothd attaches its input profile.
func remoteHID() []hidDevice {
	return []hidDevice{{
		MAC:     normalizeMAC(testRemote),
		DevPath: "/devices/virtual/misc/uhid/0005:1915:EEEE.0007",
		Nodes:   []string{"/dev/input/event7", "/dev/input/event8"},
	}}
}

// testRecovery reads its clock through a pointer the test moves, so a
// wait runs out without any waiting.
func testRecovery(radio *fakeRadio, held *relays, now *time.Time) *inputRecovery {
	return newInputRecovery(radio, held, func() time.Time { return *now })
}

// recoveryPass runs one pass at the time now points to, and waits for
// the reconnect it starts, if any.
func recoveryPass(t *testing.T, recovery *inputRecovery, radio *fakeRadio, kernel []hidDevice, unpairing map[bonds.Address]bool) inventoryPass {
	t.Helper()
	snapshot, err := radio.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	pass := inventoryPass{unpairing: unpairing}
	recovery.reconcile(snapshot, kernel, &pass)
	recovery.attempts.Wait()
	return pass
}

// A connected controller that has delivered input before, and has no
// HID device in the kernel once the grace has run out, is reconnected
// through bluetoothd. Every other controller is left alone.
func TestRecoveryReconnectsOnlyAConnectedControllerWithNoHIDDevice(t *testing.T) {
	reconnect := []string{"Disconnect " + remoteAddress(t).Key(), "Connect " + remoteAddress(t).Key()}
	cases := []struct {
		name      string
		device    deviceState
		held      func(*testing.T) *relays
		kernel    []hidDevice
		unpairing map[bonds.Address]bool
		elapsed   time.Duration
		want      []string
	}{
		{
			name:    "connected with no HID device past the grace",
			device:  connectedRemote(t),
			held:    remoteRelays,
			elapsed: recoveryGrace,
			want:    reconnect,
		},
		{
			name:    "connected with no HID device inside the grace",
			device:  connectedRemote(t),
			held:    remoteRelays,
			elapsed: recoveryGrace - time.Second,
		},
		{
			name:    "connected with its HID device",
			device:  connectedRemote(t),
			held:    remoteRelays,
			kernel:  remoteHID(),
			elapsed: recoveryGrace,
		},
		{
			name:    "asleep",
			device:  sleepingRemote(t),
			held:    remoteRelays,
			elapsed: recoveryGrace,
		},
		{
			name:    "a device that has never delivered input, such as a speaker",
			device:  connectedRemote(t),
			held:    func(*testing.T) *relays { return newRelays(newFakeKernel()) },
			elapsed: recoveryGrace,
		},
		{
			name:      "a device under teardown",
			device:    connectedRemote(t),
			held:      remoteRelays,
			unpairing: map[bonds.Address]bool{remoteAddress(t): true},
			elapsed:   recoveryGrace,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			radio := testRadio(t, c.device)
			now := testNow
			recovery := testRecovery(radio, c.held(t), &now)

			recoveryPass(t, recovery, radio, c.kernel, c.unpairing)
			now = now.Add(c.elapsed)
			recoveryPass(t, recovery, radio, c.kernel, c.unpairing)

			if !slices.Equal(radio.calls, c.want) {
				t.Fatalf("calls = %v, want %v", radio.calls, c.want)
			}
		})
	}
}

// The grace ends with no event to mark it, so a pass inside it asks
// the loop to run again when it ends.
func TestRecoveryAsksForAPassWhenTheGraceEnds(t *testing.T) {
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
// The wait doubles on each reconnect that does not help, up to a
// ceiling, so a controller that never recovers costs a reconnect a
// quarter of an hour and not one every pass.
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
			t.Fatalf("after %d waits Disconnect ran %d times: %v", c.attempt-1, got, radio.calls)
		}
		early := recoveryPass(t, recovery, radio, nil, nil)
		if early.again != c.want {
			t.Fatalf("after reconnect %d the pass asks to run again in %s, want %s", c.attempt, early.again, c.want)
		}
		now = now.Add(c.want)
	}
}

// The wait survives the moment the reconnect itself takes the link
// down, because a pass can read the device disconnected between the
// two calls. Otherwise every reconnect would start a fresh grace, and
// a controller that never recovers would be reconnected every few
// seconds.
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

// A HID device that appears ends the wait, so a remote that recovered
// and is stuck again days later is reconnected after the grace and not
// after a wait an old failure left behind.
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

// The inventory pass runs the recovery against the HID devices sysfs
// holds, so a remote whose link came up with no input node is
// reconnected without a restart of this operator.
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
