package main

// These tests cover how the pass arms the relay: the Asleep a status
// write settles on arms the remote, and the next open of its keyboard
// node writes the press. They run the real pass over the fake radio and
// the fake API server, and the relay over the fake kernel.

import (
	"bytes"
	"testing"
	"time"
)

// remotePass is an inventory over a radio that holds the sample remote,
// connected, and the relay desk of sleptRemote. Its clock is testNow.
func remotePass(t *testing.T, fixture *apiFixture) (*inventory, *fakeRadio, *relays, *fakeKernel) {
	t.Helper()
	device := connectedRemote(t)
	device.Address = testAddress(t, sampleRemote)
	radio := testRadio(t, device)
	inventory := testInventory(t, fixture, radio)
	held, kernel := sleptRemote(t, everyInputClass)
	inventory.relays = held
	return inventory, radio, held, kernel
}

// sleep takes the remote's link down with a timeout, as BlueZ reports a
// remote that went to sleep, and runs the pass.
func sleep(t *testing.T, inventory *inventory, radio *fakeRadio) {
	t.Helper()
	radio.update(testAddress(t, sampleRemote), func(d *deviceState) { d.Connected = false })
	inventory.ends.record(testAddress(t, sampleRemote), blueZReasonTimeout)
	inventory.reconcile()
}

// A pass that writes Asleep arms the remote, and its next connect an
// hour later writes the press.
func TestAPassThatWritesAsleepArmsThePress(t *testing.T) {
	inventory, radio, held, kernel := remotePass(t, newAPIFixture())
	inventory.reconcile()
	sleep(t, inventory, radio)

	held.now = func() time.Time { return testNow.Add(time.Hour) }
	held.ensure(remoteMAC, []string{remoteKeys})

	if got := virtualOf(t, kernel, remoteKeysName).received(); !bytes.Equal(got, wakePressRecords()) {
		t.Errorf("the keyboard node received %x, want one press and release of KEY_UNKNOWN", got)
	}
}

// A restart of the operator keeps Asleep on a sleeping remote, and the
// first pass of the new process arms the remote from the Peripheral.
func TestTheFirstPassAfterARestartArmsFromThePeripheral(t *testing.T) {
	fixture := newAPIFixture()
	before, radio, _, _ := remotePass(t, fixture)
	before.reconcile()
	sleep(t, before, radio)

	after, _, held, kernel := remotePass(t, fixture)
	after.radio = radio
	after.reconcile()
	held.now = func() time.Time { return testNow.Add(time.Hour) }
	held.ensure(remoteMAC, []string{remoteKeys})

	if got := virtualOf(t, kernel, remoteKeysName).received(); !bytes.Equal(got, wakePressRecords()) {
		t.Errorf("the keyboard node received %x, want one press and release of KEY_UNKNOWN", got)
	}
}

// A reason that arrives for a link that is up again by the time of the
// pass belongs to a link the pass never saw end. The pass writes LinkUp
// and forgets the reason, so a later disconnect with no signal does not
// read it as its own.
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
