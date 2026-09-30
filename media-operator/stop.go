package main

// The stop press during a film. HDMI-CEC 1.3a, CEC 13.13.3, says a TV
// remote's Stop Function stops the media and keeps it stopped when
// repeated, and the kernel's rc-cec keymap names it KEY_STOPCD. A Play
// that ended has nothing left to play, so stop ends the run and asks
// the client nothing: the unit returns to its idle screen, and a second
// press reaches no film.

// The action word for stop. The Play's commands topic accepts it.
const actionStop = "stop"

// stop ends the run by the path every ending takes.
func (c *commander) stop(trigger string) {
	logLine(c.log, "command: %s: stop, so the run ends", trigger)
	c.exit()
}
