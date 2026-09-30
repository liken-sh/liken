package main

// What a TV remote's Stop Function does to a running film. HDMI-CEC
// 1.3a, CEC 13.13.3, says it stops the media and keeps it stopped when
// repeated, and a Play that ended has nothing left to stop.

import "testing"

// A stop ends the run the way the display's exit does, and asks the
// client nothing, so the unit returns to its idle screen.
func TestAStopPressEndsTheRun(t *testing.T) {
	c, broker, lines := keyTestCommander(t)
	c.statusTopic = playStatusTopic(defaultTopicBase, keyTestPlayNS, keyTestPlayrun)
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
}
