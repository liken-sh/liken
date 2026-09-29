package main

// The home press during a film. The playback pod ends the run and asks
// on the Player's commands topic, where the client under the film reads
// the ask as a press of the home key.

// The action word for home. The Play's commands topic accepts it, and
// the Player's commands topic carries it.
const actionHome = "home"

// home publishes the ask first and ends the run after it, so the client
// is on its home page before the ending moves the unit to Idle. The
// ending is the one a back press reaches once mpv closes, so the film
// ends after the same grace.
func (c *commander) home(trigger string) {
	c.askAndEnd(trigger, actionHome)
}
