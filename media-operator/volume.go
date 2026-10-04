package main

// The listening level as the screens read it. The operator relays each
// unit's level on the Player's volume topic as {"level": 0.63, "muted":
// false}, where the level is the fraction of the device's max
// (volumeengine.go). The playback pod's command sidecar reads the topic
// and sends each live level to the display, which draws the indicator.
// mpv plays at unity, its own default, so no level reaches mpv.

import (
	"encoding/json"
	"strconv"
)

// parseRelayLevel reads one message off the topic. A payload that does
// not decode is no level at all. A level outside 0 to 1 is held inside
// it, so another program's message cannot draw a bar past either end.
func parseRelayLevel(payload []byte) (relayLevel, bool) {
	var level relayLevel
	if err := json.Unmarshal(payload, &level); err != nil {
		return relayLevel{}, false
	}
	level.Level = min(max(level.Level, 0), 1)
	return level, true
}

// volumeChangedMessage is the display's one show trigger for the
// indicator. It carries the level and the mute, and the display draws
// the bar from them and not from mpv's volume property, which stays at
// unity.
const volumeChangedMessage = "volume-changed"

// volumeChangedCommand is the script message for one live level: the
// level as a decimal from 0.0 to 1.0, and the mute as yes or no.
func volumeChangedCommand(level relayLevel) []any {
	return []any{"script-message", volumeChangedMessage,
		strconv.FormatFloat(level.Level, 'f', -1, 64), mpvYesNo(level.Muted)}
}

// mpvYesNo writes a flag the way mpv reads one, on the command line
// and on the IPC socket alike.
func mpvYesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
