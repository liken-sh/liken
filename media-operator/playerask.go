package main

// Some commands end the film and need an action that the playback pod
// cannot take, such as home, which shows the home page of the idle
// screen's client. The pod publishes such a command on the Player's
// commands topic, and that client acts on it.

import (
	"encoding/json"
	"fmt"
	"os"
)

// publishCommandAndEnd publishes command and then ends the run. The bus sends the
// command before the ending, so the client receives it before the Player
// status changes to Idle.
func (c *commander) publishCommandAndEnd(trigger string, command mediaCommand) {
	if topic, published := c.publishCommand(command); published {
		logLine(c.log, "command: %s: %s, published the %s command to %s, so the run ends", trigger, command.Action, command.Action, topic)
	} else {
		logLine(c.log, "command: %s: %s, with no Player commands topic to publish on, so the run ends", trigger, command.Action)
	}
	c.exit()
}

// publishCommand publishes command on the Player's commands topic and returns
// the topic. It does not retain the message, because a command is an
// event and not a state. It returns false when the pod read no Player or
// has no bus.
func (c *commander) publishCommand(command mediaCommand) (string, bool) {
	if c.playerCommandsTopic == "" || c.bus == nil {
		return "", false
	}
	payload, err := json.Marshal(command)
	if err != nil {
		fmt.Fprintf(os.Stderr, "command: encoding the %s command: %v\n", command.Action, err)
		return "", false
	}
	c.bus.Publish(c.playerCommandsTopic, payload, false)
	return c.playerCommandsTopic, true
}
