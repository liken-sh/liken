package main

import (
	"testing"
	"testing/synctest"
)

// A KEY_STOPCD press publishes the run's ending and quits mpv.
func TestAStopPressEndsTheRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, broker, lines := keyTestCommander(t)
		c.statusTopic = playStatusTopic(defaultTopicBase, keyTestPlayNS, keyTestPlay)
		c.playerCommandsTopic = playerCommandsTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayer)
		c.lastReport = playReport{Item: 1, Position: "0:20:00"}
		c.haveReport = true
		events, _ := keyTestTopics()
		focusHere(c)

		c.handle(events, mustEncode(t, keyEvent{Key: "KEY_STOPCD", Value: 1}))

		ending := waitForPublish(t, broker.pubs)
		mustMatch(t, ending.topic, c.statusTopic)
		mustMatch(t, endedReport(t, ending.payload), playReport{Item: 1, Position: "0:20:00", Ended: true})
		mustMatch(t, nextLine(t, lines), `{"command":["quit","0"]}`)
	})
}
