package main

// The session writer: every write of a Receiver's status.session
// composes the session and the newest ask of each kind. The fake API
// server is in operate_test.go.

import (
	"errors"
	"testing"
	"time"
)

// Two asks of one kind in the same millisecond carry two times, so the
// equipment operator reads them as two asks.
func TestTwoAsksInOneMillisecondCarryTwoTimes(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 15, 25, 164_000_000, time.UTC)
	first := nextAskTime("", now)

	mustMatch(t, first, "2026-10-04T12:15:25.164Z")
	mustMatch(t, nextAskTime(first, now), "2026-10-04T12:15:25.165Z")
	mustMatch(t, nextAskTime(first, now.Add(time.Second)), "2026-10-04T12:15:26.164Z")
}

// An ask for a Receiver that holds no session of this operator's writes
// nothing, because the ask has no unit to belong to.
func TestAnAskWithNoSessionWritesNothing(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	err := media.sessions.ask(media.client, "den-receiver", func(asks *receiverAsks) {
		asks.power = &ReceiverAction{Action: "toggle", At: "2026-10-04T12:20:01.002Z"}
	})

	mustMatch(t, errors.Is(err, errNoSession), true)
	mustMatch(t, len(cluster.sessions), 0)
}

// A new session carries the asks the Receiver already holds, so a
// change of the active flag removes no ask. A lift removes the asks
// with the session, and the next session starts with none.
func TestTheSessionCarriesTheAsksUntilItIsLifted(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	session := ReceiverSession{Player: "house/theater", Input: "GAME", Awake: true}
	mustSucceed(t, media.sessions.apply(media.client, "den-receiver", session))
	mustSucceed(t, media.sessions.ask(media.client, "den-receiver", func(asks *receiverAsks) {
		asks.power = &ReceiverAction{Action: "toggle", At: "2026-10-04T12:20:01.002Z"}
	}))

	session.Active = true
	mustSucceed(t, media.sessions.apply(media.client, "den-receiver", session))
	mustMatch(t, cluster.sessions[2].session.PowerAsk.Action, "toggle")

	mustSucceed(t, media.sessions.lift(media.client, "den-receiver"))
	mustSucceed(t, media.sessions.apply(media.client, "den-receiver", session))
	mustMatch(t, cluster.sessions[4].session.PowerAsk == nil, true)
}
