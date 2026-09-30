package main

// What a power press does to a running film.

import "testing"

// The ask reaches the Player's commands topic before the ending does, so
// the client holds the ask when the unit's Idle status arrives.
func TestAPowerPressPublishesTheAskAndThenEndsTheRun(t *testing.T) {
	cases := []struct{ key, ask string }{
		{key: "KEY_POWER", ask: `{"action":"power"}`},
		{key: "KEY_POWER2", ask: `{"action":"power"}`},
		// A TV remote's Power Off Function asks for off, which leaves a room
		// that is already off as it is.
		{key: "KEY_SLEEP", ask: `{"action":"power-off"}`},
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
			mustMatch(t, string(ask.payload), each.ask)
			ending := waitForPublish(t, broker.pubs)
			mustMatch(t, ending.topic, c.statusTopic)
			mustMatch(t, endedReport(t, ending.payload), playReport{Item: 1, Position: "0:20:00", Ended: true})
			mustMatch(t, nextLine(t, lines), `{"command":["quit","0"]}`)
		})
	}
}

// A pod that read no Player publishes no ask and still ends the film.
func TestAPowerPressWithNoPlayerEndsTheRunAndAsksNothing(t *testing.T) {
	c, broker, lines := keyTestCommander(t)
	c.statusTopic = playStatusTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayrun)
	c.lastReport = playReport{Item: 1, Position: "0:20:00"}
	c.haveReport = true
	events, _ := keyTestTopics()
	focusHere(c)

	c.handle(events, mustEncode(t, keyEvent{Key: "KEY_POWER", Value: 1}))

	ending := waitForPublish(t, broker.pubs)
	mustMatch(t, ending.topic, c.statusTopic)
	mustMatch(t, endedReport(t, ending.payload), playReport{Item: 1, Position: "0:20:00", Ended: true})
	mustMatch(t, nextLine(t, lines), `{"command":["quit","0"]}`)
}

// A power command on the Play's commands topic runs the path a press runs.
func TestAPowerCommandOnThePlaysTopicEndsTheRun(t *testing.T) {
	c, broker, lines := keyTestCommander(t)
	c.statusTopic = playStatusTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayrun)
	c.playerCommandsTopic = playerCommandsTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayer)
	c.lastReport = playReport{Item: 1}
	c.haveReport = true

	c.handle(c.commandsTopic, mustEncode(t, mediaCommand{Action: actionPower}))

	ask := waitForPublish(t, broker.pubs)
	mustMatch(t, ask.topic, c.playerCommandsTopic)
	mustMatch(t, string(ask.payload), `{"action":"power"}`)
	mustMatch(t, nextLine(t, lines), `{"command":["quit","0"]}`)
}
