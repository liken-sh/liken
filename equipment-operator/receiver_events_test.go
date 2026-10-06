package main

// The Events of a Receiver's Reachable condition, against a fake Denon
// that drops its connections.

import (
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/events"
)

// A receiver that drops off the network posts one Warning, and one
// Normal when it answers again. The recorder counts the second
// Connected on the first, because the two are the same Event within
// ten minutes.
func TestAReceiverThatDropsOffPostsUnreachableAndConnected(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		receiver := startFakeDenon(t)
		api.setReceivers(testReceiver("theater", receiver.address()))
		operator := startController(t, api)
		mustSucceed(t, operator.pass(t.Context()))
		api.waitForStatus(t, connected)

		receiver.dropConnections()
		api.waitForStatus(t, func(status ReceiverStatus) bool { return status.Conditions[0].Status == ConditionFalse })
		api.waitForStatus(t, connected)
		operator.stopAll()

		mustDeepEqual(t, withoutConnecting(postedAbout(api.events, "Receiver", "theater")), []posted{
			{events.TypeNormal, reasonConnected, 2},
			{events.TypeWarning, reasonUnreachable, 1},
		})
	})
}

// withoutConnecting drops the Event of the first write, which says
// Connecting only when the write runs before the receiver answers.
func withoutConnecting(list []posted) []posted {
	var out []posted
	for _, one := range list {
		if one.reason != reasonConnecting {
			out = append(out, one)
		}
	}
	return out
}
