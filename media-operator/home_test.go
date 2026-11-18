package main

import (
	"testing"
	"testing/synctest"
)

// The home command reaches the Player's commands topic before the
// ending does.
func TestAHomePressPublishesTheHomeCommandAndThenEndsTheRun(t *testing.T) {
	cases := []struct{ key string }{
		{key: "KEY_HOMEPAGE"},
		{key: "KEY_WWW"},
	}
	for _, each := range cases {
		t.Run(each.key, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, broker, lines := keyTestCommander(t)
				c.statusTopic = playStatusTopic(defaultTopicBase, keyTestPlayNS, keyTestPlay)
				c.playerCommandsTopic = playerCommandsTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayer)
				c.lastReport = playReport{Item: 1, Position: "0:20:00"}
				c.haveReport = true
				events, _ := keyTestTopics()
				focusHere(c)

				c.handle(events, mustEncode(t, keyEvent{Key: each.key, Value: 1}))

				ask := waitForPublish(t, broker.pubs)
				mustMatch(t, ask.topic, c.playerCommandsTopic)
				mustMatch(t, ask.retained, false)
				// The command names the Remote, so the operator does not
				// ask the receiver to show the Player a second time. It
				// already did that for the press on the events topic.
				mustMatch(t, string(ask.payload), `{"action":"home","remote":"`+remoteOfTopic(events)+`"}`)
				ending := waitForPublish(t, broker.pubs)
				mustMatch(t, ending.topic, c.statusTopic)
				mustMatch(t, endedReport(t, ending.payload), playReport{Item: 1, Position: "0:20:00", Ended: true})
				mustMatch(t, nextLine(t, lines), `{"command":["quit","0"]}`)
			})
		})
	}
}

// A pod that read no Player publishes no home command and still ends
// the film.
func TestAHomePressWithNoPlayerEndsTheRunAndPublishesNoCommand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, broker, lines := keyTestCommander(t)
		c.statusTopic = playStatusTopic(defaultTopicBase, keyTestPlayNS, keyTestPlay)
		c.lastReport = playReport{Item: 1, Position: "0:20:00"}
		c.haveReport = true
		events, _ := keyTestTopics()
		focusHere(c)

		c.handle(events, mustEncode(t, keyEvent{Key: "KEY_HOMEPAGE", Value: 1}))

		ending := waitForPublish(t, broker.pubs)
		mustMatch(t, ending.topic, c.statusTopic)
		mustMatch(t, endedReport(t, ending.payload), playReport{Item: 1, Position: "0:20:00", Ended: true})
		mustMatch(t, nextLine(t, lines), `{"command":["quit","0"]}`)
	})
}

// A home command on the Play's commands topic takes the same path as a
// press.
func TestAHomeCommandOnThePlaysTopicEndsTheRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, broker, lines := keyTestCommander(t)
		c.statusTopic = playStatusTopic(defaultTopicBase, keyTestPlayNS, keyTestPlay)
		c.playerCommandsTopic = playerCommandsTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayer)
		c.lastReport = playReport{Item: 1}
		c.haveReport = true

		c.handle(c.commandsTopic, mustEncode(t, mediaCommand{Action: actionHome}))

		ask := waitForPublish(t, broker.pubs)
		mustMatch(t, ask.topic, c.playerCommandsTopic)
		mustMatch(t, string(ask.payload), `{"action":"home"}`)
		mustMatch(t, nextLine(t, lines), `{"command":["quit","0"]}`)
	})
}
