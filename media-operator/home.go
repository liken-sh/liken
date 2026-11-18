package main

// A home press during a film ends the run, and the playback pod
// publishes a home command on the Player's commands topic. The client
// that draws the screen between films then shows its home page.

const actionHome = "home"

// home publishes the home command and then ends the run. remote is the
// Remote whose press triggered it, or empty when a program published
// home on the Play's commands topic.
func (c *commander) home(trigger, remote string) {
	c.publishCommandAndEnd(trigger, mediaCommand{Action: actionHome, Remote: remote})
}
