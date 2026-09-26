package main

// The home press during a film. The playback pod ends the run and asks
// on the Player's commands topic, where the client under the film reads
// the ask as a press of the home key.

import (
	"encoding/json"
	"fmt"
	"os"
)

// The action word for home. The Play's commands topic accepts it, and
// the Player's commands topic carries it.
const actionHome = "home"

// home publishes the ask first and ends the run after it, so the client
// is on its home page before the ending moves the unit to Idle. The
// ending is the one a back press reaches once mpv closes, so the film
// ends after the same grace.
func (c *commander) home(trigger string) {
	if topic, published := c.publishHome(); published {
		logLine(c.log, "command: %s: home, published the home ask to %s, so the run ends", trigger, topic)
	} else {
		logLine(c.log, "command: %s: home, with no player commands topic to ask on, so the run ends", trigger)
	}
	c.exit()
}

// publishHome publishes one message on the Player's commands topic, not
// retained, because the ask is an event and not a state. A pod that read
// no Player has no topic to publish on, and a pod with no bus has no
// broker, so each publishes nothing. The answer names the topic and
// whether the ask went out.
func (c *commander) publishHome() (string, bool) {
	if c.playerCommandsTopic == "" || c.bus == nil {
		return "", false
	}
	payload, err := json.Marshal(mediaCommand{Action: actionHome})
	if err != nil {
		fmt.Fprintf(os.Stderr, "command: home: %v\n", err)
		return "", false
	}
	c.bus.Publish(c.playerCommandsTopic, payload, false)
	return c.playerCommandsTopic, true
}
