package main

// The operator publishes each Player's level on the Player's volume topic
// as {"level": 0.63, "muted": false}, where level is a fraction of the
// device's maximum (volumeengine.go). When a Receiver draws its own
// overlay on the TV, the payload also has "indicator": "receiver". The
// playback pod's command sidecar reads the topic and sends each level to
// the display, which draws the indicator. The sidecar never sets mpv's
// volume, which stays at its default of 100.

import (
	"encoding/json"
	"strconv"
)

// parseRelayLevel decodes one message from the topic, or returns false.
// It clamps the level to 0 through 1, so a message from another program
// cannot draw a bar past either end.
func parseRelayLevel(payload []byte) (relayLevel, bool) {
	var level relayLevel
	if err := json.Unmarshal(payload, &level); err != nil {
		return relayLevel{}, false
	}
	level.Level = min(max(level.Level, 0), 1)
	return level, true
}

// volumeChangedMessage is the mpv script message that sends a level to
// the display. The display draws the bar from its arguments, not from
// mpv's volume property.
const volumeChangedMessage = "volume-changed"

// volumeChangedCommand builds the mpv command for one level. Its
// arguments are the level from 0.0 to 1.0, the mute as yes or no, and
// whether to draw the bar as yes or no. The display records the level
// and the mute even when it does not draw the bar.
func volumeChangedCommand(level relayLevel, draw bool) []any {
	return []any{"script-message", volumeChangedMessage,
		strconv.FormatFloat(level.Level, 'f', -1, 64), mpvYesNo(level.Muted), mpvYesNo(draw)}
}

// drawsAfter reports whether the display draws the bar for this level.
// before is the previous level, and hadBefore is false when there was
// none. When the Receiver draws its own overlay, the display draws no
// bar, so the TV does not show two. A message that differs from the
// previous one only in its indicator field is the operator republishing
// the retained level after spec.volume.indicator or the device changed.
// No person changed the level, so the display draws no bar. A press at
// the end of the scale repeats the previous payload exactly and does
// draw.
func (l relayLevel) drawsAfter(before relayLevel, hadBefore bool) bool {
	if l.Indicator == relayIndicatorReceiver {
		return false
	}
	return !hadBefore || l.Indicator == before.Indicator || l.Level != before.Level || l.Muted != before.Muted
}

// mpvYesNo returns a boolean as mpv writes it, "yes" or "no".
func mpvYesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
