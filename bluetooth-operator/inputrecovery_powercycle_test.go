package main

// When bluetoothd does not answer the Disconnect of a stuck controller,
// the operator powers the adapter off and on: which calls it makes, how
// often, and the Event it posts on the controller's Peripheral.

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

// stuckRemote is a remote past the grace with no HID device, and a
// recovery that is due to act on it at the next pass.
func stuckRemote(t *testing.T) (*inputRecovery, *fakeRadio, *time.Time) {
	t.Helper()
	radio := testRadio(t, connectedRemote(t))
	now := testNow
	recovery := testRecovery(radio, remoteRelays(t), &now)
	recoveryPass(t, recovery, radio, nil, nil)
	now = now.Add(recoveryGrace)
	return recovery, radio, &now
}

// A Disconnect that bluetoothd does not answer leaves the remote's link
// state stale, and a Connect after it would return at once and do
// nothing. So the operator powers the adapter off and then on, and
// makes no Connect. A Disconnect that works keeps the reconnect.
func TestAFailedDisconnectPowerCyclesTheAdapter(t *testing.T) {
	remote := remoteAddress(t).Key()
	cases := []struct {
		name          string
		disconnectErr error
		want          []string
	}{
		{
			name:          "bluetoothd sends no reply",
			disconnectErr: context.DeadlineExceeded,
			want:          []string{"Disconnect " + remote, "SetAdapterPowered false", "SetAdapterPowered true"},
		},
		{
			name: "the disconnect works",
			want: []string{"Disconnect " + remote, "Connect " + remote},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			recovery, radio, _ := stuckRemote(t)
			radio.disconnectErr = c.disconnectErr

			recoveryPass(t, recovery, radio, nil, nil)

			if !slices.Equal(radio.calls, c.want) {
				t.Fatalf("calls = %v, want %v", radio.calls, c.want)
			}
		})
	}
}

// A power cycle drops every link on the adapter, so it is not repeated
// at once when the remote is stuck again. It waits out the same
// doubling wait as a reconnect.
func TestRepeatedPowerCyclesFollowTheRecoveryWait(t *testing.T) {
	recovery, radio, now := stuckRemote(t)
	radio.disconnectErr = context.DeadlineExceeded
	recoveryPass(t, recovery, radio, nil, nil)

	cases := []struct {
		after time.Duration
		want  int
	}{
		{firstRecoveryRetry - time.Second, 1},
		{time.Second, 2},
		{2*firstRecoveryRetry - time.Second, 2},
		{time.Second, 3},
	}
	for _, c := range cases {
		// bluetoothd reports the remote Connected again with no HID
		// device, which is the stale state that started the recovery.
		radio.update(remoteAddress(t), func(state *deviceState) { state.Connected = true })
		*now = now.Add(c.after)
		recoveryPass(t, recovery, radio, nil, nil)
		if got := radio.counted("SetAdapterPowered false"); got != c.want {
			t.Fatalf("after %s more the adapter powered off %d times, want %d: %v", c.after, got, c.want, radio.calls)
		}
	}
}

// recoveryEvents gives the recovery a recorder that writes to a fake of
// the events collection. The caller runs in a synctest bubble.
func recoveryEvents(t *testing.T, recovery *inputRecovery) *eventstest.Events {
	t.Helper()
	recorded := &eventstest.Events{}
	server := apiservertest.Start(t, recorded.Around(newAPIFixture().handler(t)))
	client := apiclient.New(apiservertest.Host, server.Client(), "")
	recovery.recorder = events.New(t.Context(), client, component, events.Options{Log: io.Discard})
	return recorded
}

// A power cycle is an action that changes no condition, so it posts one
// Event on the stuck controller's Peripheral. An adapter that bluetoothd
// powered off and does not power on again needs a person, so that
// posts a Warning. A power-off that failed changed nothing and posts
// nothing.
func TestAPowerCyclePostsOneEventOnThePeripheral(t *testing.T) {
	cases := []struct {
		name        string
		powerOffErr error
		powerOnErr  error
		want        []posted
	}{
		{
			name: "the adapter powers off and on",
			want: []posted{{"Normal", reasonAdapterPowerCycled,
				"bluetoothd did not disconnect the controller (context deadline exceeded), so the operator powered the adapter off and on. Every link on the adapter dropped, and each bonded device can connect again."}},
		},
		{
			name:        "the power-off fails",
			powerOffErr: errors.New("org.bluez.Error.Busy"),
		},
		{
			name:       "the power-on fails",
			powerOnErr: errors.New("org.bluez.Error.Failed"),
			want: []posted{{"Warning", reasonAdapterPowerOnFailed,
				"the operator powered the adapter off to recover the controller, and bluetoothd did not power it on again: org.bluez.Error.Failed. Delete the operator's pod on this node to power the adapter on."}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				recovery, radio, _ := stuckRemote(t)
				recorded := recoveryEvents(t, recovery)
				radio.disconnectErr = context.DeadlineExceeded
				radio.powerOffErr, radio.powerOnErr = c.powerOffErr, c.powerOnErr

				recoveryPass(t, recovery, radio, nil, nil)

				assertPosted(t, postedAbout(recorded, peripheralKind, "fe-5c-63-a3-ff-2e"), c.want...)
			})
		})
	}
}
