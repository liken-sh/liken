package main

// These tests cover the press the relay writes when a sleeping remote
// connects, over the fake kernel in inputkernel_test.go. What they
// assert is what a consumer of the remote's claim reads on the virtual
// node: one press and one release of KEY_UNKNOWN, or nothing.

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"
)

// The remote's two evdev nodes. The first holds keyboard codes, and the
// second holds consumer keys above BTN_MISC only, the way a gamepad's
// buttons are.
const (
	remoteMAC        = "c2:11:22:33:44:55"
	remoteKeys       = "/dev/input/event10"
	remoteButtons    = "/dev/input/event11"
	remoteKeysAgain  = "/dev/input/event12"
	remoteKeysName   = "T6-Remote Keyboard"
	remoteButtonName = "T6-Remote Buttons"
)

// sleptAt is when the pass wrote Asleep for the remote.
var sleptAt = testNow.Add(-time.Hour)

// asleep is the Connected condition a pass writes for a remote whose
// link timed out at a given time.
func asleep(at time.Time) Condition {
	return Condition{Type: conditionConnected, Status: conditionFalse, Reason: reasonAsleep, LastTransitionTime: at}
}

// sleptRemote is a relay desk over a fake kernel that answers for the
// remote's two nodes, with its clock at testNow and a prepared claim
// that demands the classes given.
func sleptRemote(t *testing.T, demand inputClasses) (*relays, *fakeKernel) {
	t.Helper()
	kernel := newFakeKernel()
	kernel.capabilities[remoteKeys] = evdevCapabilities{
		Name:  remoteKeysName,
		ID:    evdevID{Bus: 0x0005},
		Codes: map[string][]uint16{"EV_KEY": {0x67, 0x6c}, "EV_MSC": {0x04}},
	}
	// A reconnect registers the same node on a new number.
	kernel.capabilities[remoteKeysAgain] = kernel.capabilities[remoteKeys]
	kernel.capabilities[remoteButtons] = evdevCapabilities{
		Name:  remoteButtonName,
		ID:    evdevID{Bus: 0x0005},
		Codes: map[string][]uint16{"EV_KEY": {0x130, 0x131}},
	}
	held := newRelays(kernel)
	held.metrics = newMetrics()
	held.now = func() time.Time { return testNow }
	t.Cleanup(func() { held.stop(remoteMAC) })
	if demand != 0 {
		held.prepare(remoteMAC, "claim-1", delivery{classes: demand})
	}
	return held, kernel
}

// wakePressRecords is what the relay writes for one wake: a press and a
// release of KEY_UNKNOWN, each followed by SYN_REPORT.
func wakePressRecords() []byte {
	return bytes.Join([][]byte{
		rawEvent(evKey, keyUnknown, 1),
		rawEvent(evSyn, synReport, 0),
		rawEvent(evKey, keyUnknown, 0),
		rawEvent(evSyn, synReport, 0),
	}, nil)
}

// virtualOf answers the virtual device the relay made for a node name.
func virtualOf(t *testing.T, kernel *fakeKernel, name string) *fakeVirtual {
	t.Helper()
	kernel.mu.Lock()
	defer kernel.mu.Unlock()
	for _, device := range kernel.virtual {
		if device.caps.Name == name {
			return device
		}
	}
	t.Fatalf("the relay made no virtual device for %q", name)
	return nil
}

// An armed remote that slept for longer than 10 seconds gets one press
// on its first keyboard node, and the node with no keyboard code gets
// none. The press disarms the remote, so a node opened again gets none.
func TestAWakingRemoteGetsOnePressOnItsKeyboardNode(t *testing.T) {
	held, kernel := sleptRemote(t, everyInputClass)
	held.follow(remoteMAC, asleep(sleptAt))

	held.ensure(remoteMAC, []string{remoteButtons, remoteKeys})
	kernel.writer(t, remoteKeys).Close()
	waitFor(t, "the relay to open the node again", func() bool {
		held.ensure(remoteMAC, []string{remoteButtons, remoteKeysAgain})
		return kernel.opened() == 3
	})

	if got := virtualOf(t, kernel, remoteKeysName).received(); !bytes.Equal(got, wakePressRecords()) {
		t.Errorf("the keyboard node received %x, want one press and release of KEY_UNKNOWN", got)
	}
	if got := virtualOf(t, kernel, remoteButtonName).received(); len(got) != 0 {
		t.Errorf("the node with no keyboard code received %x, want nothing", got)
	}
	if count, found := metricValue(t, held.metrics.registry,
		"bluetooth_wake_presses_total", map[string]string{"peripheral": "c2-11-22-33-44-55"}); !found || count != 1 {
		t.Errorf("bluetooth_wake_presses_total = %v (found: %v), want 1", count, found)
	}
}

// Each case opens the remote's keyboard node once, and the relay writes
// nothing into it.
func TestTheRelayWritesNoPress(t *testing.T) {
	cases := []struct {
		name   string
		demand inputClasses
		arm    func(*relays)
	}{
		{
			name:   "a remote that is not armed",
			demand: everyInputClass,
			arm:    func(*relays) {},
		},
		{
			name:   "a remote that connects within 10 seconds of the sleep",
			demand: everyInputClass,
			arm:    func(r *relays) { r.follow(remoteMAC, asleep(testNow.Add(-9*time.Second))) },
		},
		{
			name:   "a remote that a pass found connected",
			demand: everyInputClass,
			arm: func(r *relays) {
				r.follow(remoteMAC, asleep(sleptAt))
				r.follow(remoteMAC, Condition{Type: conditionConnected, Status: conditionTrue, Reason: reasonLinkUp, LastTransitionTime: testNow})
			},
		},
		{
			name:   "a remote whose link the radio closed",
			demand: everyInputClass,
			arm: func(r *relays) {
				r.follow(remoteMAC, asleep(sleptAt))
				r.follow(remoteMAC, Condition{Type: conditionConnected, Status: conditionFalse, Reason: reasonClosedByRadio, LastTransitionTime: sleptAt})
			},
		},
		{
			name:   "a remote no claim holds",
			demand: 0,
			arm:    func(r *relays) { r.follow(remoteMAC, asleep(sleptAt)) },
		},
		{
			name:   "a remote whose claim masks out EV_KEY",
			demand: classSwitch,
			arm:    func(r *relays) { r.follow(remoteMAC, asleep(sleptAt)) },
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			held, kernel := sleptRemote(t, c.demand)
			c.arm(held)

			held.ensure(remoteMAC, []string{remoteKeys})

			if got := virtualOf(t, kernel, remoteKeysName).received(); len(got) != 0 {
				t.Errorf("the keyboard node received %x, want nothing", got)
			}
		})
	}
}

// A pass writes the same Asleep, with the same lastTransitionTime, on
// each pass while the remote sleeps, and after the press. Arming again
// from the same sleep gives no second press.
func TestASleepGivesOnePressHoweverOftenAPassArmsIt(t *testing.T) {
	held, kernel := sleptRemote(t, everyInputClass)
	held.follow(remoteMAC, asleep(sleptAt))
	held.ensure(remoteMAC, []string{remoteKeys})
	kernel.writer(t, remoteKeys).Close()
	held.follow(remoteMAC, asleep(sleptAt))

	waitFor(t, "the relay to open the node again", func() bool {
		held.ensure(remoteMAC, []string{remoteKeysAgain})
		return kernel.opened() == 2
	})

	if got := virtualOf(t, kernel, remoteKeysName).received(); !bytes.Equal(got, wakePressRecords()) {
		t.Errorf("the keyboard node received %x, want one press and release of KEY_UNKNOWN", got)
	}
}

// The kernel drops a key that a virtual device did not declare, and a
// uinput device cannot declare one after it is created. So a node with
// keyboard codes declares KEY_UNKNOWN, and a node with buttons only does
// not.
func TestAVirtualDeviceWithKeyboardCodesDeclaresKeyUnknown(t *testing.T) {
	cases := []struct {
		name  string
		caps  evdevCapabilities
		codes []uint16
	}{
		{"a keyboard node", evdevCapabilities{Codes: map[string][]uint16{"EV_KEY": {0x67, 0x6c}}}, []uint16{0x67, 0x6c, keyUnknown}},
		{"a keyboard node that reports KEY_UNKNOWN itself", evdevCapabilities{Codes: map[string][]uint16{"EV_KEY": {0x67, keyUnknown}}}, []uint16{0x67, keyUnknown}},
		{"a gamepad node", evdevCapabilities{Codes: map[string][]uint16{"EV_KEY": {0x130, 0x131}}}, []uint16{0x130, 0x131}},
		{"a node with no keys", evdevCapabilities{Codes: map[string][]uint16{"EV_ABS": {0x00}}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			codes := declaredCodes(c.caps, "EV_KEY")

			if !slices.Equal(codes, c.codes) {
				t.Errorf("declared %v, want %v", codes, c.codes)
			}
		})
	}
}

// The press changes nothing that is stored: the snapshot in the bond's
// Secret still holds the codes the real node reports.
func TestThePressDeclaresNothingInTheStoredSnapshot(t *testing.T) {
	held, _ := sleptRemote(t, everyInputClass)
	held.ensure(remoteMAC, []string{remoteKeys})

	if snapshot := string(held.snapshot(remoteMAC)); strings.Contains(snapshot, "240") {
		t.Errorf("the snapshot holds KEY_UNKNOWN: %s", snapshot)
	}
}
