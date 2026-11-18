package main

// These tests cover the wake press through a whole reconcile pass. When
// a pass sets the reason of a Peripheral's Connected condition to
// Asleep, the relay schedules a wake press. The next time the relay opens the remote's
// keyboard node, at least wakeAfter after the link ended, it writes one
// press and release of KEY_UNKNOWN. The tests run the real pass against
// the fake radio and the fake API server, and the real relays against
// the fake kernel.

import (
	"bytes"
	"testing"
	"time"
)

// remotePass returns an inventory whose fake radio has the sample remote
// connected, with the relays and the fake kernel from sleptRemote. Its
// clock reads testNow.
func remotePass(t *testing.T, fixture *apiFixture) (*inventory, *fakeRadio, *relays, *fakeKernel) {
	t.Helper()
	device := connectedRemote(t)
	device.Address = testAddress(t, sampleRemote)
	radio := testRadio(t, device)
	inventory := testInventory(t, fixture, radio)
	relay, kernel := sleptRemote(t, everyInputClass)
	inventory.relays = relay
	return inventory, radio, relay, kernel
}

// sleep disconnects the remote with a timeout reason, which is how BlueZ
// reports a remote that went to sleep, and runs a pass.
func sleep(t *testing.T, inventory *inventory, radio *fakeRadio) {
	t.Helper()
	radio.update(testAddress(t, sampleRemote), func(d *deviceState) { d.Connected = false })
	inventory.ends.record(testAddress(t, sampleRemote), blueZReasonTimeout)
	inventory.reconcile()
}

// After a pass writes Asleep, the remote's next connect writes the press.
func TestAPassThatWritesAsleepSchedulesThePress(t *testing.T) {
	inventory, radio, relay, kernel := remotePass(t, newAPIFixture())
	inventory.reconcile()
	sleep(t, inventory, radio)

	// An hour later is past wakeAfter.
	relay.now = func() time.Time { return testNow.Add(time.Hour) }
	relay.ensure(remoteMAC, []string{remoteKeys})

	if got := virtualOf(t, kernel, remoteKeysName).received(); !bytes.Equal(got, wakePressRecords()) {
		t.Errorf("the keyboard node received %x, want one press and release of KEY_UNKNOWN", got)
	}
}

// Asleep stays in the Peripheral status across an operator restart, so
// the first pass of the new process schedules the press from it.
func TestTheFirstPassAfterARestartSchedulesThePressFromThePeripheral(t *testing.T) {
	fixture := newAPIFixture()
	before, radio, _, _ := remotePass(t, fixture)
	before.reconcile()
	sleep(t, before, radio)

	after, _, relay, kernel := remotePass(t, fixture)
	after.radio = radio
	after.reconcile()
	relay.now = func() time.Time { return testNow.Add(time.Hour) }
	relay.ensure(remoteMAC, []string{remoteKeys})

	if got := virtualOf(t, kernel, remoteKeysName).received(); !bytes.Equal(got, wakePressRecords()) {
		t.Errorf("the keyboard node received %x, want one press and release of KEY_UNKNOWN", got)
	}
}

// A Disconnected signal can arrive when the link is already up again. A
// pass that finds the remote connected writes LinkUp and deletes the
// stored reason, so a later disconnect with no signal reports
// NotConnected and not this old Timeout.
func TestAPassThatFindsTheRemoteConnectedForgetsTheReason(t *testing.T) {
	fixture := newAPIFixture()
	inventory, radio, _, _ := remotePass(t, fixture)
	inventory.ends.record(testAddress(t, sampleRemote), blueZReasonTimeout)
	inventory.reconcile()

	radio.update(testAddress(t, sampleRemote), func(d *deviceState) { d.Connected = false })
	inventory.reconcile()

	if got := reason(read[Peripheral](t, fixture, peripheralPath("c2-11-22-33-44-55")), conditionConnected); got != reasonNotConnected {
		t.Errorf("reason = %q, want %q for a disconnect with no signal", got, reasonNotConnected)
	}
}
