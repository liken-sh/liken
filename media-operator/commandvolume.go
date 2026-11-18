package main

// mpv plays at full scale, and the receiver or the sinks set the level.
// The operator publishes the level on the Player's volume topic, so the
// sidecar can draw a volume bar over the film for every change, from any
// controller or from the receiver's knob.

// showVolume sends a level from the volume topic to mpv, with whether
// mpv draws the bar for it.
func (c *commander) showVolume(payload []byte) {
	level, ok := parseRelayLevel(payload)
	if !ok {
		return
	}
	draw := level.drawsAfter(c.lastLevel, c.haveLevel)
	c.lastLevel, c.haveLevel = level, true
	c.command(volumeChangedCommand(level, draw))
}

// recordVolume stores the retained level without sending it to mpv, so
// that showVolume compares the first live level with it.
func (c *commander) recordVolume(payload []byte) {
	if level, ok := parseRelayLevel(payload); ok {
		c.lastLevel, c.haveLevel = level, true
	}
}
