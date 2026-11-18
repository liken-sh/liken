package main

import (
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/events"
)

// The second Connected Event has count 2, because the recorder patches
// the count of an identical Event written in the last ten minutes.
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

// withoutConnecting removes the Connecting Event. The first status
// write posts it only when the write happens before the receiver
// responds, so the test cannot depend on it.
func withoutConnecting(list []posted) []posted {
	var out []posted
	for _, one := range list {
		if one.reason != reasonConnecting {
			out = append(out, one)
		}
	}
	return out
}
