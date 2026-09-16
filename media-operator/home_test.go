package main

// What a home press does to a running film.

import "testing"

// The ask reaches the Player's commands topic before the ending does.
func TestAHomePressPublishesTheAskAndThenEndsTheRun(t *testing.T) {
	cases := []struct{ key string }{
		{key: "KEY_HOMEPAGE"},
		{key: "KEY_WWW"},
	}
	for _, each := range cases {
		t.Run(each.key, func(t *testing.T) {
			c, broker, lines := keyTestCommander(t)
			c.statusTopic = playStatusTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayrun)
			c.playerCommandsTopic = playerCommandsTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayer)
			c.lastReport = playReport{Item: 1, Position: "0:20:00"}
			c.haveReport = true
			events, _ := keyTestTopics()
			focusHere(c)

			c.handle(events, mustEncode(t, keyEvent{Key: each.key, Value: 1}))

			ask := waitForPublish(t, broker.pubs)
			mustMatch(t, ask.topic, c.playerCommandsTopic)
			mustMatch(t, ask.retained, false)
			mustMatch(t, string(ask.payload), `{"action":"home"}`)
			ending := waitForPublish(t, broker.pubs)
			mustMatch(t, ending.topic, c.statusTopic)
			mustMatch(t, endedReport(t, ending.payload), playReport{Item: 1, Position: "0:20:00", Ended: true})
			mustMatch(t, nextLine(t, lines), `{"command":["quit","0"]}`)
		})
	}
}

// A pod that read no Player publishes no ask and still ends the film.
func TestAHomePressWithNoPlayerEndsTheRunAndAsksNothing(t *testing.T) {
	c, broker, lines := keyTestCommander(t)
	c.statusTopic = playStatusTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayrun)
	c.lastReport = playReport{Item: 1, Position: "0:20:00"}
	c.haveReport = true
	events, _ := keyTestTopics()
	focusHere(c)

	c.handle(events, mustEncode(t, keyEvent{Key: "KEY_HOMEPAGE", Value: 1}))

	ending := waitForPublish(t, broker.pubs)
	mustMatch(t, ending.topic, c.statusTopic)
	mustMatch(t, endedReport(t, ending.payload), playReport{Item: 1, Position: "0:20:00", Ended: true})
	mustMatch(t, nextLine(t, lines), `{"command":["quit","0"]}`)
}

// A home command on the Play's commands topic runs the path a press runs.
func TestAHomeCommandOnThePlaysTopicEndsTheRun(t *testing.T) {
	c, broker, lines := keyTestCommander(t)
	c.statusTopic = playStatusTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayrun)
	c.playerCommandsTopic = playerCommandsTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayer)
	c.lastReport = playReport{Item: 1}
	c.haveReport = true

	c.handle(c.commandsTopic, mustEncode(t, mediaCommand{Action: actionHome}))

	ask := waitForPublish(t, broker.pubs)
	mustMatch(t, ask.topic, c.playerCommandsTopic)
	mustMatch(t, string(ask.payload), `{"action":"home"}`)
	mustMatch(t, nextLine(t, lines), `{"command":["quit","0"]}`)
}
