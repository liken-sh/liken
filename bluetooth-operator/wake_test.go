package main

// These tests run the relay against the fake kernel in
// inputkernel_test.go and check what a consumer reads from the virtual
// node when a sleeping remote reconnects: one press and release of
// KEY_UNKNOWN, or nothing.

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"
)

// The remote's evdev nodes. remoteKeys reports keyboard codes, and
// remoteButtons reports only codes above BTN_MISC, like the buttons of a
// gamepad. remoteKeysAgain is the keyboard node after a reconnect, which
// the kernel registers under a new number.
const (
	remoteMAC        = "c2:11:22:33:44:55"
	remoteKeys       = "/dev/input/event10"
	remoteButtons    = "/dev/input/event11"
	remoteKeysAgain  = "/dev/input/event12"
	remoteKeysName   = "T6-Remote Keyboard"
	remoteButtonName = "T6-Remote Buttons"
)

// sleptAt is the lastTransitionTime of the Asleep condition.
var sleptAt = testNow.Add(-time.Hour)

// asleep returns the Connected condition that a pass writes for a
// remote whose link timed out at the given time.
func asleep(at time.Time) Condition {
	return Condition{Type: conditionConnected, Status: conditionFalse, Reason: reasonAsleep, LastTransitionTime: at}
}

// sleptRemote returns relays on a fake kernel that has the remote's
// nodes, with the clock at testNow. When classes is not zero, a
// prepared claim requests those input classes.
func sleptRemote(t *testing.T, classes inputClasses) (*relays, *fakeKernel) {
	t.Helper()
	kernel := newFakeKernel()
	kernel.capabilities[remoteKeys] = evdevCapabilities{
		Name:  remoteKeysName,
		ID:    evdevID{Bus: 0x0005},
		Codes: map[string][]uint16{"EV_KEY": {0x67, 0x6c}, "EV_MSC": {0x04}},
	}
	kernel.capabilities[remoteKeysAgain] = kernel.capabilities[remoteKeys]
	kernel.capabilities[remoteButtons] = evdevCapabilities{
		Name:  remoteButtonName,
		ID:    evdevID{Bus: 0x0005},
		Codes: map[string][]uint16{"EV_KEY": {0x130, 0x131}},
	}
	relay := newRelays(kernel)
	relay.metrics = newMetrics()
	relay.now = func() time.Time { return testNow }
	t.Cleanup(func() { relay.stop(remoteMAC) })
	if classes != 0 {
		relay.prepare(remoteMAC, "claim-1", inputSettings{classes: classes})
	}
	return relay, kernel
}

// wakePressRecords returns the bytes of one press and one release of
// KEY_UNKNOWN, each followed by SYN_REPORT.
func wakePressRecords() []byte {
	return bytes.Join([][]byte{
		rawEvent(evKey, keyUnknown, 1),
		rawEvent(evSyn, synReport, 0),
		rawEvent(evKey, keyUnknown, 0),
		rawEvent(evSyn, synReport, 0),
	}, nil)
}

// virtualOf returns the virtual device that the relay created for the
// node with the given name.
func virtualOf(t *testing.T, kernel *fakeKernel, name string) *fakeVirtual {
	t.Helper()
	kernel.mu.Lock()
	defer kernel.mu.Unlock()
	for _, device := range kernel.virtual {
		if device.caps.Name == name {
			return device
		}
	}
	t.Fatalf("the relay created no virtual device named %q, want one", name)
	return nil
}

// A remote that slept for longer than wakeAfter gets one press on
// its keyboard node, and the node with no keyboard code gets none. The
// press disarms the remote, so the node opened again after a reconnect
// gets none.
func TestAWakingRemoteGetsOnePressOnItsKeyboardNode(t *testing.T) {
	relay, kernel := sleptRemote(t, everyInputClass)
	relay.follow(remoteMAC, asleep(sleptAt))

	relay.ensure(remoteMAC, []string{remoteButtons, remoteKeys})
	kernel.writer(t, remoteKeys).Close()
	waitFor(t, "the relay to open the node again", func() bool {
		relay.ensure(remoteMAC, []string{remoteButtons, remoteKeysAgain})
		return kernel.opened() == 3
	})

	if got := virtualOf(t, kernel, remoteKeysName).received(); !bytes.Equal(got, wakePressRecords()) {
		t.Errorf("the keyboard node received %x, want one press and release of KEY_UNKNOWN", got)
	}
	if got := virtualOf(t, kernel, remoteButtonName).received(); len(got) != 0 {
		t.Errorf("the node with no keyboard code received %x, want nothing", got)
	}
	if count, found := metricValue(t, relay.metrics.registry,
		"bluetooth_wake_presses_total", map[string]string{"peripheral": "c2-11-22-33-44-55"}); !found || count != 1 {
		t.Errorf("bluetooth_wake_presses_total = %v (found: %v), want 1", count, found)
	}
}

// In each case the relay opens the remote's keyboard node and writes
// nothing to it.
func TestTheRelayWritesNoPress(t *testing.T) {
	cases := []struct {
		name    string
		classes inputClasses
		arm     func(*relays)
	}{
		{
			name:    "a remote that is not armed",
			classes: everyInputClass,
			arm:     func(*relays) {},
		},
		{
			name:    "a remote that connects within 10 seconds of the sleep",
			classes: everyInputClass,
			arm:     func(r *relays) { r.follow(remoteMAC, asleep(testNow.Add(-9*time.Second))) },
		},
		{
			name:    "a remote that a pass found connected",
			classes: everyInputClass,
			arm: func(r *relays) {
				r.follow(remoteMAC, asleep(sleptAt))
				r.follow(remoteMAC, Condition{Type: conditionConnected, Status: conditionTrue, Reason: reasonLinkUp, LastTransitionTime: testNow})
			},
		},
		{
			name:    "a remote whose link the radio closed",
			classes: everyInputClass,
			arm: func(r *relays) {
				r.follow(remoteMAC, asleep(sleptAt))
				r.follow(remoteMAC, Condition{Type: conditionConnected, Status: conditionFalse, Reason: reasonClosedByRadio, LastTransitionTime: sleptAt})
			},
		},
		{
			name:    "a remote no claim holds",
			classes: 0,
			arm:     func(r *relays) { r.follow(remoteMAC, asleep(sleptAt)) },
		},
		{
			name:    "a remote whose claim masks out EV_KEY",
			classes: classSwitch,
			arm:     func(r *relays) { r.follow(remoteMAC, asleep(sleptAt)) },
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			relay, kernel := sleptRemote(t, c.classes)
			c.arm(relay)

			relay.ensure(remoteMAC, []string{remoteKeys})

			if got := virtualOf(t, kernel, remoteKeysName).received(); len(got) != 0 {
				t.Errorf("the keyboard node received %x, want nothing", got)
			}
		})
	}
}

// Every pass while the remote sleeps, and after the press, reports the
// same Asleep with the same lastTransitionTime. Arming again from the
// same sleep writes no second press.
func TestASleepGivesOnePressHoweverOftenAPassReportsIt(t *testing.T) {
	relay, kernel := sleptRemote(t, everyInputClass)
	relay.follow(remoteMAC, asleep(sleptAt))
	relay.ensure(remoteMAC, []string{remoteKeys})
	kernel.writer(t, remoteKeys).Close()
	relay.follow(remoteMAC, asleep(sleptAt))

	waitFor(t, "the relay to open the node again", func() bool {
		relay.ensure(remoteMAC, []string{remoteKeysAgain})
		return kernel.opened() == 2
	})

	if got := virtualOf(t, kernel, remoteKeysName).received(); !bytes.Equal(got, wakePressRecords()) {
		t.Errorf("the keyboard node received %x, want one press and release of KEY_UNKNOWN", got)
	}
}

// The kernel drops a key that a virtual device did not declare, and a
// uinput device cannot declare one after it is created. So the virtual
// device of a node with keyboard codes declares KEY_UNKNOWN, and the
// virtual device of a node with only buttons does not.
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

// The snapshot in the bond's Secret has only the codes that the real
// node reports, without the KEY_UNKNOWN that the virtual device adds.
func TestThePressDeclaresNothingInTheStoredSnapshot(t *testing.T) {
	relay, _ := sleptRemote(t, everyInputClass)
	relay.ensure(remoteMAC, []string{remoteKeys})

	if snapshot := string(relay.snapshot(remoteMAC)); strings.Contains(snapshot, "240") {
		t.Errorf("the snapshot is %s, want no KEY_UNKNOWN", snapshot)
	}
}
