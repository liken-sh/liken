package main

// Tests of a Television's status.session when the API server refuses a
// request. The API server returns 429 for a short time after a CRD is
// created, and other refusals can last longer, so a session that the
// operator could not write is written on a later pass.

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

// sessionOperator returns a controller with one idle Receiver whose
// input GAME is on Display acm-0001-receiver, a display that the status
// of Television lounge lists.
func sessionOperator(t *testing.T, api *cecAPI) *controller {
	t.Helper()
	amp := startFakeDenon(t)
	receiver := idleReceiver(amp.address(), ReceiverVolume{Max: 69.5})
	receiver.Spec.Inputs = []ReceiverInput{{Name: "GAME", Machine: "node-1", Monitor: "acm-0001-receiver"}}
	api.putReceiver(receiver)
	api.showing(lounge(""), "acm-0001-receiver")
	operator := newController(api.client, testMetrics(t))
	operator.dial = testNetwork.dial
	operator.listen = testNetwork.bind
	operator.log = &logBuffer{}
	t.Cleanup(operator.stopAll)
	return operator
}

// adoptedSession is the status.session that the first pass writes for
// the idle session.
var adoptedSession = &TelevisionSession{Player: "house/theater", Display: "acm-0001-receiver"}

// A Television list that the API server throttles once does not stop the
// pass from adopting the session.
func TestAThrottledTelevisionListStillAdoptsTheSession(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		operator := sessionOperator(t, api)
		api.mutex.Lock()
		api.throttledTelevisionLists = 1
		api.mutex.Unlock()

		mustSucceed(t, operator.pass(t.Context()))

		television, _ := api.television("lounge")
		mustDeepEqual(t, television.Status.Session, adoptedSession)
	})
}

// A session write that the API server refuses is written on the next
// pass, with the session that the first pass adopted.
func TestARefusedSessionWriteIsWrittenOnALaterPass(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		operator := sessionOperator(t, api)
		api.mutex.Lock()
		api.noSessionWrites = true
		api.mutex.Unlock()
		mustSucceed(t, operator.pass(t.Context()))
		api.mutex.Lock()
		api.noSessionWrites = false
		api.mutex.Unlock()

		mustSucceed(t, operator.pass(t.Context()))

		television, _ := api.television("lounge")
		mustDeepEqual(t, television.Status.Session, adoptedSession)
	})
}

// After stop, a list or an apply sends no request and returns the
// stop's error, context.Canceled. So a timer that clears a Television's
// session and fires during shutdown writes nothing after the Deployment
// has given up its Lease.
func TestNoSessionRequestLeavesAfterStop(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := &cannedAPI{}
		sessions := newTelevisionSessions(testAPIClient(t, api.handler()))

		sessions.stop()

		if _, err := sessions.list(); !errors.Is(err, context.Canceled) {
			t.Errorf("list after stop returned %v, want context.Canceled", err)
		}
		if err := sessions.apply("lounge", adoptedSession); !errors.Is(err, context.Canceled) {
			t.Errorf("apply after stop returned %v, want context.Canceled", err)
		}
		mustMatch(t, api.sent(), 0)
	})
}
