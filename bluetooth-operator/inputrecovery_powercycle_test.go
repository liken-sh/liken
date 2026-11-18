package main

// These tests cover the recovery of a stuck controller when the
// Disconnect through bluetoothd fails, and the operator powers the
// adapter off and on instead.

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

// stuckRemote returns a recovery, a radio, and a clock for a remote that
// is connected and has had no HID device for recoveryGrace, so the next
// recoveryPass acts on it.
func stuckRemote(t *testing.T) (*inputRecovery, *fakeRadio, *time.Time) {
	t.Helper()
	radio := testRadio(t, connectedRemote(t))
	now := testNow
	recovery := testRecovery(radio, remoteRelays(t), &now)
	recoveryPass(t, recovery, radio, nil, nil)
	now = now.Add(recoveryGrace)
	return recovery, radio, &now
}

// After a failed Disconnect, bluetoothd still reports the remote
// connected and returns success for a Connect without connecting. So
// the operator powers the adapter off and on and does not call Connect.
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
				t.Fatalf("the radio received the calls %v, want %v", radio.calls, c.want)
			}
		})
	}
}

// A power cycle drops every link on the adapter, so it must not repeat
// right away. It runs inside a reconnect, so the doubling wait between
// reconnects also limits it.
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
		// Set the remote back to the state that started the recovery:
		// Connected, with no HID device.
		radio.update(remoteAddress(t), func(state *deviceState) { state.Connected = true })
		*now = now.Add(c.after)
		recoveryPass(t, recovery, radio, nil, nil)
		if got := radio.counted("SetAdapterPowered false"); got != c.want {
			t.Fatalf("%s later the adapter has been powered off %d times, want %d, calls: %v", c.after, got, c.want, radio.calls)
		}
	}
}

// recoveryEvents sets the recovery's recorder to write to a fake events
// API and returns what it records. The caller must run in a synctest
// bubble.
func recoveryEvents(t *testing.T, recovery *inputRecovery) *eventstest.Events {
	t.Helper()
	recorded := &eventstest.Events{}
	server := apiservertest.Start(t, recorded.Around(newAPIFixture().handler(t)))
	client := apiclient.New(apiservertest.Host, server.Client(), "")
	recovery.recorder = events.New(t.Context(), client, component, events.Options{Log: io.Discard})
	return recorded
}

// A power cycle changes no condition, so the operator posts one Event
// on the stuck controller's Peripheral. A failed power-on posts a
// Warning, because a person has to act. A failed power-off changed
// nothing and posts no Event.
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
				"bluetoothd did not disconnect the controller (context deadline exceeded), so the operator powered the adapter off and on. Every link on the adapter dropped, and each bonded device can connect again"}},
		},
		{
			name:        "the power-off fails",
			powerOffErr: errors.New("org.bluez.Error.Busy"),
		},
		{
			name:       "the power-on fails",
			powerOnErr: errors.New("org.bluez.Error.Failed"),
			want: []posted{{"Warning", reasonAdapterPowerOnFailed,
				"The operator powered the adapter off to recover the controller, and bluetoothd did not power it on again: org.bluez.Error.Failed. Delete the operator's pod on this node to power the adapter on"}},
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
