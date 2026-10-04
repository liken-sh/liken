package main

// The command sidecar's controller half. The events topic carries key
// names, so nothing stands between a controller and the pod that owns
// mpv's socket, and one container holds every controller the unit
// names. The focus mark is still the gate: it names a Player and never
// a Play, and a controller pointed at another room reaches this film
// not at all.

import "encoding/json"

// A playRemote is one of the unit's controllers as this sidecar reads
// it: the retained focus topic it gates on. The events topic is the
// map's key, because that is what an inbound message carries.
type playRemote struct {
	focus string
}

// playRemoteMap pairs each remote's events topic with the focus topic
// on the same line of the second list. The operator joins both lists
// with newlines and keeps them aligned by position, the same shape the
// idle screen client reads.
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

// subscribeRemotes makes the two subscriptions each controller needs.
// Both are made once, because the Bus re-sends every filter on a
// reconnect. The focus topic is retained, so the gate stands before
// the first press.
func (c *commander) subscribeRemotes(bus *Bus) {
	for events, remote := range c.remotes {
		bus.Subscribe(events)
		if remote.focus != "" {
			bus.Subscribe(remote.focus)
		}
	}
}

// handleRemote answers the topics the controllers own and reports
// whether this message was one of them, so the caller reads the
// commands topic only for a message no controller sent.
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

// key turns one key event into what this pod does with it. A press
// this Play's Player does not hold the mark for does nothing. A key
// with no row does nothing. The cycle key asks the operator to move
// the mark and never reaches mpv.
//
// The press is what a person did, so the press earns one line: the
// key, the Remote, and what the press did or why it did nothing. A
// repeat acts the same and writes no line, and the release of a held
// key writes one line with the count of repeats it acted on.
func (c *commander) key(topic string, remote playRemote, payload []byte) {
	var event keyEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return
	}
	// The operator reads the volume keys off the same events topic, sets
	// the room's level, and writes the press's line, so the sidecar
	// does nothing with them and writes no line of its own.
	if _, theOperators := volumeKeys[event.Key]; theOperators {
		return
	}
	trigger := event.Key + " from remote " + remoteOfTopic(topic)
	if event.Value == 0 {
		c.release(topic, trigger, event.Key)
		return
	}
	if mark, holds := c.focusMark(topic); !holds {
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
		c.hold(topic, event.Key)
	}
	c.apply(trigger, command, repeat)
}

// focusElsewhere says why a press did nothing when the mark does not
// name this pod's Player.
func focusElsewhere(mark string) string {
	if mark == "" {
		return "no focus mark names a player for this remote"
	}
	return "focus is on player " + mark
}

// hold counts one repeat a held control acted on.
func (c *commander) hold(topic, key string) {
	c.focusMu.Lock()
	defer c.focusMu.Unlock()
	if c.holds == nil {
		c.holds = map[string]int{}
	}
	c.holds[topic+" "+key]++
}

// release ends one hold, and logs the count when the hold repeated. A
// release with no repeats before it is the ordinary tap, and its press
// already has its line.
func (c *commander) release(topic, trigger, key string) {
	c.focusMu.Lock()
	repeats := c.holds[topic+" "+key]
	delete(c.holds, topic+" "+key)
	c.focusMu.Unlock()
	if repeats > 0 {
		logLine(c.log, "command: %s released after %s", trigger, countOf(repeats, "repeat", "repeats"))
	}
}

// focusCycleSuffix turns a remote's focus topic into its cycle topic, the
// same path remoteFocusCycleTopic builds, so the sidecar needs no second
// topic list.
const focusCycleSuffix = "/cycle"

// publishCycle sends the cycle request the operator arbitrates, on
// the remote's own cycle topic, not retained, because a cycle is an
// event and not a state. It is the same message the idle screen client
// publishes between films.
func (c *commander) publishCycle(trigger string, remote playRemote) {
	if remote.focus == "" {
		logLine(c.log, "command: %s ignored, because the remote has no focus topic", trigger)
		return
	}
	c.bus.Publish(remote.focus+focusCycleSuffix, nil, false)
	logLine(c.log, "command: %s: cycle focus, published the cycle request to %s", trigger, remote.focus+focusCycleSuffix)
}

// setFocus records one controller's mark. The gate is set on every
// message, catch-up and live alike, because this pod draws nothing on
// a mark and only reads it.
func (c *commander) setFocus(events, mark string) {
	c.focusMu.Lock()
	defer c.focusMu.Unlock()
	if c.marks == nil {
		c.marks = map[string]string{}
	}
	c.marks[events] = mark
}

// focusMark reads this controller's mark, and reports whether it names
// the Player this Play runs on. A sidecar that read no Player name
// matches no mark and answers no press.
func (c *commander) focusMark(events string) (string, bool) {
	c.focusMu.Lock()
	defer c.focusMu.Unlock()
	mark := c.marks[events]
	return mark, c.playerName != "" && mark == c.playerName
}

// remoteForFocus reports which controller a focus topic marks. The
// list is the unit's own controllers, so the scan is over a handful.
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
