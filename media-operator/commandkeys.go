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
// idle command pod reads.
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
func (c *commander) key(topic string, remote playRemote, payload []byte) {
	var event keyEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return
	}
	if !c.holdsFocus(topic) {
		return
	}
	command, bound := commandForKey(event)
	if !bound {
		return
	}
	if command.Action == actionCycleFocus {
		c.publishCycle(remote)
		return
	}
	c.apply(command)
}

// publishCycle sends the cycle request the operator arbitrates, on
// the remote's own cycle topic, not retained, because a cycle is an
// event and not a state. It is the same message the idle command pod
// publishes between films.
func (c *commander) publishCycle(remote playRemote) {
	if remote.focus == "" {
		return
	}
	c.bus.Publish(remote.focus+focusCycleSuffix, nil, false)
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

// holdsFocus reports whether this controller's mark names the Player
// this Play runs on. A sidecar that read no Player name matches no
// mark and answers no press.
func (c *commander) holdsFocus(events string) bool {
	c.focusMu.Lock()
	defer c.focusMu.Unlock()
	return c.playerName != "" && c.marks[events] == c.playerName
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
