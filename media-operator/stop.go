package main

// actionStop is the action of a command on the Play's commands topic.
const actionStop = "stop"

// stop ends the run, for the Stop function of a remote (HDMI-CEC 1.3a,
// section CEC 13.13.3), which the kernel's rc-cec keymap maps to
// KEY_STOPCD. Unlike home, it publishes nothing on the Player's commands
// topic, so the Player returns to its idle screen.
func (c *commander) stop(trigger string) {
	logLine(c.log, "command: %s: stop, so the run ends", trigger)
	c.exit()
}
