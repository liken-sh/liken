package main

// The command sidecar reads the key events of every controller of its
// Player and writes the matching commands to mpv's socket. Each
// controller has a focus mark, a retained message with the name of the
// Player it drives. The sidecar acts on a key only when the mark is this
// sidecar's Player.

import "encoding/json"

// A playRemote is one controller of this Play's Player, and focus is the
// topic of its focus mark. The sidecar keys its playRemotes by events
// topic, because that is what an incoming message gives it.
type playRemote struct {
	focus string
}

// playRemoteMap pairs each events topic with the focus topic at the
// same position. The operator writes both lists to the pod's environment
// joined with newlines, in the same order.
func playRemoteMap(events, focuses string) map[string]playRemote {
	remotes := map[string]playRemote{}
	focusList := splitTopicLines(focuses)
	for index, topic := range splitTopicLines(events) {
		if topic == "" {
			continue
		}
		remote := playRemote{}
		if index < len(focusList) {
			remote.focus = focusList[index]
		}
		remotes[topic] = remote
	}
	return remotes
}

// subscribeRemotes runs once, because the Bus subscribes to every
// filter again after a reconnect. The broker retains the focus mark, so
// the current mark arrives right after the subscription.
func (c *commander) subscribeRemotes(bus *Bus) {
	for events, remote := range c.remotes {
		bus.Subscribe(events)
		if remote.focus != "" {
			bus.Subscribe(remote.focus)
		}
	}
}

// handleRemote handles a message on a controller's events or focus
// topic, and returns false for any other topic so that the caller can
// handle it as a command.
func (c *commander) handleRemote(topic string, payload []byte) bool {
	if remote, ours := c.remotes[topic]; ours {
		c.key(topic, remote, payload)
		return true
	}
	if events, ours := c.remoteForFocus(topic); ours {
		c.setFocus(events, string(payload))
		return true
	}
	return false
}

// key handles one key event from a controller. It ignores the key when
// the focus mark is not this Play's Player or when the playback table
// has no row for the key. The cycle key publishes a focus cycle request
// and sends nothing to mpv.
//
// Each press writes one log line with what it did or why it did nothing.
// A repeat writes no line, and the release of a held key writes one line
// with the number of repeats that acted.
func (c *commander) key(topic string, remote playRemote, payload []byte) {
	var event keyEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return
	}
	// The operator reads the volume keys from the same topic, sets the
	// volume, and logs the press, so the sidecar ignores them.
	if _, isVolumeKey := volumeKeys[event.Key]; isVolumeKey {
		return
	}
	trigger := event.Key + " from remote " + remoteOfTopic(topic)
	if event.Value == 0 {
		c.release(topic, trigger, event.Key)
		return
	}
	if mark, focused := c.focusMark(topic); !focused {
		if event.Value == 1 {
			logLine(c.log, "command: %s ignored, because %s", trigger, focusElsewhere(mark))
		}
		return
	}
	command, bound := commandForKey(event)
	if !bound {
		if event.Value == 1 {
			logLine(c.log, "command: %s ignored, because the playback table has no row for it", trigger)
		}
		return
	}
	if command.Action == actionCycleFocus {
		c.publishCycle(trigger, remote)
		return
	}
	repeat := event.Value == 2
	if repeat {
		c.countRepeat(topic, event.Key)
	}
	command.Remote = remoteOfTopic(topic)
	c.apply(trigger, command, repeat)
}

func focusElsewhere(mark string) string {
	if mark == "" {
		return "no focus mark names a player for this remote"
	}
	return "focus is on player " + mark
}

// countRepeat counts one repeat of a held key that acted.
func (c *commander) countRepeat(topic, key string) {
	c.focusMu.Lock()
	defer c.focusMu.Unlock()
	if c.repeatCounts == nil {
		c.repeatCounts = map[string]int{}
	}
	c.repeatCounts[topic+" "+key]++
}

// release logs the number of repeats of a held key. A tap has no
// repeats, and its press already has a log line.
func (c *commander) release(topic, trigger, key string) {
	c.focusMu.Lock()
	repeats := c.repeatCounts[topic+" "+key]
	delete(c.repeatCounts, topic+" "+key)
	c.focusMu.Unlock()
	if repeats > 0 {
		logLine(c.log, "command: %s released after %s", trigger, countOf(repeats, "repeat", "repeats"))
	}
}

// The sidecar appends focusCycleSuffix to a focus topic to get the
// topic that remoteFocusCycleTopic builds, so the operator does not pass
// it a list of cycle topics.
const focusCycleSuffix = "/cycle"

// publishCycle publishes a request for the operator to move the
// controller's focus to the next Player. The message is not retained,
// because a cycle request is an event and not a state. The idle screen
// client publishes the same request when it has focus.
func (c *commander) publishCycle(trigger string, remote playRemote) {
	if remote.focus == "" {
		logLine(c.log, "command: %s ignored, because the remote has no focus topic", trigger)
		return
	}
	c.bus.Publish(remote.focus+focusCycleSuffix, nil, false)
	logLine(c.log, "command: %s: cycle focus, published the cycle request to %s", trigger, remote.focus+focusCycleSuffix)
}

// setFocus stores every focus mark, the retained one and each later
// change, because the sidecar reads the mark only when a key arrives.
func (c *commander) setFocus(events, mark string) {
	c.focusMu.Lock()
	defer c.focusMu.Unlock()
	if c.marks == nil {
		c.marks = map[string]string{}
	}
	c.marks[events] = mark
}

// focusMark returns the controller's mark and whether it is this Play's
// Player. A sidecar with no Player name ignores every press.
func (c *commander) focusMark(events string) (string, bool) {
	c.focusMu.Lock()
	defer c.focusMu.Unlock()
	mark := c.marks[events]
	return mark, c.playerName != "" && mark == c.playerName
}

func (c *commander) remoteForFocus(topic string) (string, bool) {
	if topic == "" {
		return "", false
	}
	for events, remote := range c.remotes {
		if remote.focus == topic {
			return events, true
		}
	}
	return "", false
}
