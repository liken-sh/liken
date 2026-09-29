package main

// The session path against an API server that is not ready. A new CRD
// makes the API server answer 429 for a moment, and any other refusal
// can last longer, so a Television's status.session that the first
// try could not write is written on a later pass.

import (
	"context"
	"errors"
	"testing"
)

// sessionOperator is the Deployment over one Receiver whose idle
// session's input carries Display acm-0001-receiver, which Television
// lounge lists.
func sessionOperator(t *testing.T, api *cecAPI) *controller {
	t.Helper()
	amp := startFakeDenon(t)
	brokers := startFakeBrokerServer(t)
	receiver := idleReceiver(amp.address(), ReceiverVolume{Max: 69.5})
	receiver.Spec.Inputs = []ReceiverInput{{Name: "GAME", Machine: "node-1", Monitor: "acm-0001-receiver"}}
	api.putReceiver(receiver)
	api.showing(lounge(""), "acm-0001-receiver")
	operator := newController(api.client, brokers.address(), testMetrics(t))
	operator.log = &logBuffer{}
	t.Cleanup(operator.stopAll)
	return operator
}

// adoptedSession is what the first pass writes for the idle session.
var adoptedSession = &TelevisionSession{Player: "house/theater", Display: "acm-0001-receiver"}

func TestAThrottledTelevisionListStillAdoptsTheSession(t *testing.T) {
	api := startCECAPI(t)
	operator := sessionOperator(t, api)
	api.mutex.Lock()
	api.throttledTelevisionLists = 1
	api.mutex.Unlock()

	mustSucceed(t, operator.pass(t.Context()))

	television, _ := api.television("lounge")
	mustDeepEqual(t, television.Status.Session, adoptedSession)
}

// A session write the API server refuses is written on the next pass,
// as the adoption the first pass decided on: the pass that retries it
// is live, and it still wakes nothing.
func TestARefusedSessionWriteIsWrittenOnALaterPass(t *testing.T) {
	t.Parallel()
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
}

// A stop ends the sessions' requests: a list or a write after stop
// sends nothing and answers the stop, so a lift whose timer fires at a
// shutdown writes nothing after the Deployment gave up its Lease.
func TestNoSessionRequestLeavesAfterStop(t *testing.T) {
	t.Parallel()
	api := &cannedAPI{}
	sessions := newTelevisionSessions(testAPIClient(t, api.handler()))

	sessions.stop()

	if _, err := sessions.list(); !errors.Is(err, context.Canceled) {
		t.Errorf("list after stop answered %v, want the stop", err)
	}
	if err := sessions.apply("lounge", adoptedSession); !errors.Is(err, context.Canceled) {
		t.Errorf("apply after stop answered %v, want the stop", err)
	}
	mustMatch(t, api.sent(), 0)
}
