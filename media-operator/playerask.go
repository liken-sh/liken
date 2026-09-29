package main

// The asks a run makes of the client under it as the run ends. Home and
// power each end the film, and each has a meaning the playback pod cannot
// act on: the client's home page, and the room's power. So the pod asks
// on the Player's commands topic, and the client that draws the screen
// between films answers.

import (
	"encoding/json"
	"fmt"
	"os"
)

// askAndEnd publishes the ask and then runs the ending path, so the ask
// reaches the broker before the ending does, and the client holds the ask
// before the unit's status reads Idle.
func (c *commander) askAndEnd(trigger, action string) {
	if topic, published := c.publishAsk(action); published {
		logLine(c.log, "command: %s: %s, published the %s ask to %s, so the run ends", trigger, action, action, topic)
	} else {
		logLine(c.log, "command: %s: %s, with no player commands topic to ask on, so the run ends", trigger, action)
	}
	c.exit()
}

// publishAsk publishes one message on the Player's commands topic, not
// retained, because the ask is an event and not a state. A pod that read
// no Player has no topic to publish on, and a pod with no bus has no
// broker, so each publishes nothing. The answer names the topic and
// whether the ask went out.
func (c *commander) publishAsk(action string) (string, bool) {
	if c.playerCommandsTopic == "" || c.bus == nil {
		return "", false
	}
	payload, err := json.Marshal(mediaCommand{Action: action})
	if err != nil {
		fmt.Fprintf(os.Stderr, "command: %s: %v\n", action, err)
		return "", false
	}
	c.bus.Publish(c.playerCommandsTopic, payload, false)
	return c.playerCommandsTopic, true
}
