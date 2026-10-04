package main

// The command sidecar's volume half. The sidecar sets no level: the
// receiver or the sinks set the room's level, and mpv plays at unity.
// The sidecar reads the level the operator relays on the Player's
// volume topic, and sends the display each live level, so the indicator
// is drawn over the film for a press on any of the unit's controllers
// and for a turn of a receiver's knob.

// showVolume sends the display one live level off the volume topic,
// with whether the display draws the bar for it. The retained level the
// broker delivers at subscribe is not a press, so it never reaches here
// (handleRetained).
func (c *commander) showVolume(payload []byte) {
	level, ok := parseRelayLevel(payload)
	if !ok {
		return
	}
	draw := level.drawsAfter(c.heardLevel, c.heardAny)
	c.heardLevel, c.heardAny = level, true
	c.command(volumeChangedCommand(level, draw))
}

// recordVolume keeps the retained level, so the first live message that
// changes only the indicator draws nothing.
func (c *commander) recordVolume(payload []byte) {
	if level, ok := parseRelayLevel(payload); ok {
		c.heardLevel, c.heardAny = level, true
	}
}
