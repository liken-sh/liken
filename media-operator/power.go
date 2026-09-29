package main

// The power press during a film. The playback pod ends the run and asks
// on the Player's commands topic. The client under the film holds the
// ask until the unit's status reads Idle, and then answers it the way it
// answers a power press between films: with a Receiver it publishes the
// room's toggle, and with none it lowers the shade. The toggle waits for
// Idle because the equipment operator decides off or on from the room it
// reads, and an ending that reached the room after the toggle would
// change that room under the next press.

// The action word for power. The Play's commands topic accepts it, and
// the Player's commands topic carries it.
const actionPower = "power"

// power publishes the ask first and ends the run after it, the same order
// home keeps, so the client holds the ask before the ending moves the
// unit to Idle.
func (c *commander) power(trigger string) {
	c.askAndEnd(trigger, actionPower)
}
