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
func (c *commander) home() {
	c.publishHome()
	c.exit()
}

// publishHome publishes one message on the Player's commands topic, not
// retained, because the ask is an event and not a state. A pod that read
// no Player has no topic to publish on, and a pod with no bus has no
// broker, so each publishes nothing.
func (c *commander) publishHome() {
	if c.playerCommandsTopic == "" || c.bus == nil {
		return
	}
	payload, err := json.Marshal(mediaCommand{Action: actionHome})
	if err != nil {
		fmt.Fprintf(os.Stderr, "command: home: %v\n", err)
		return
	}
	c.bus.Publish(c.playerCommandsTopic, payload, false)
}
