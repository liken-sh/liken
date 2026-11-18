package main

import (
	"errors"
	"testing"
	"time"
)

func TestTwoAsksInOneMillisecondCarryTwoTimes(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 15, 25, 164_000_000, time.UTC)
	first := nextAskTime("", now)

	mustMatch(t, first, "2026-10-04T12:15:25.164Z")
	mustMatch(t, nextAskTime(first, now), "2026-10-04T12:15:25.165Z")
	mustMatch(t, nextAskTime(first, now.Add(time.Second)), "2026-10-04T12:15:26.164Z")
}

func TestAnAskWithNoSessionWritesNothing(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	err := media.sessions.ask(media.client, "den-receiver", func(asks *receiverAsks) {
		asks.power = &ReceiverAction{Action: "toggle", At: "2026-10-04T12:20:01.002Z"}
	})

	mustMatch(t, errors.Is(err, errNoSession), true)
	mustMatch(t, len(cluster.sessions), 0)
}

// A new session keeps the cached requests, so changing the active flag
// removes none. A lift removes them with the session.
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
