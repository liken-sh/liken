package main

// The Deployment's relay of an ask for a Player's screen. The node
// workload hears the bus ask for the screen, such as the TV's Set
// Stream Path for a sleeping Player's Display, and writes the ask in
// the Television's status.screenAsk (cecnode_screen.go). The node
// workload has no broker connection, and the Deployment's session for
// the Player has one, so the Deployment publishes the ask on the
// session's power topic. The Player's screen client reads that topic:
// it publishes the room's power there, and the equipment operator
// answers with the screen the room needs.

import (
	"encoding/json"
	"fmt"
	"os"
)

// The power topic's actions that the equipment operator publishes for
// the Player's screen: wake lifts the screen and asks for its panel,
// and sleep lowers it and turns the panel off.
var screenActions = map[string]string{
	screenWake:  "wake",
	screenSleep: "sleep",
}

// screenRelay is how the Television pass reaches a Player's session.
type screenRelay interface {
	askScreen(player, screen, trigger string)
}

// screenMemory is what the Television pass holds about the asks it
// relayed. listed says the pass has read the Televisions once, and seen
// maps each Television to the last ask the pass read there.
type screenMemory struct {
	listed bool
	seen   map[string]TelevisionScreenAsk
}

// relayScreenAsks asks the Player's screen once for each new ask a
// Television's status holds. An ask that the first pass finds is
// older than the Deployment's start, and a person may have done
// anything since, so it relays nothing for it.
func (c *cecBusController) relayScreenAsks(televisions []Television) {
	first := !c.screenAsks.listed
	c.screenAsks.listed = true
	if c.screenAsks.seen == nil {
		c.screenAsks.seen = map[string]TelevisionScreenAsk{}
	}
	for _, television := range televisions {
		ask := television.Status.ScreenAsk
		if ask == nil {
			continue
		}
		name := television.Metadata.Name
		if held, seen := c.screenAsks.seen[name]; seen && held == *ask {
			continue
		}
		c.screenAsks.seen[name] = *ask
		if first || c.screens == nil {
			continue
		}
		c.screens.askScreen(ask.Player, ask.Screen, fmt.Sprintf("Television %s: %s", name, ask.Cause))
	}
}

// attach records the session that holds a Player's room, so an ask for
// the Player's screen reaches it.
func (t *televisionSessions) attach(player string, held *session) {
	if t == nil {
		return
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.screens == nil {
		t.screens = map[string]*session{}
	}
	t.screens[player] = held
}

// detach forgets a session that ended. A newer session of the same
// Player that attached first stays.
func (t *televisionSessions) detach(player string, held *session) {
	if t == nil {
		return
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.screens[player] == held {
		delete(t.screens, player)
	}
}

// askScreen hands an ask for a Player's screen to the Player's session.
// A Player with no session in this operator has no power topic to ask
// on, so the ask goes nowhere, and the log says so.
func (t *televisionSessions) askScreen(player, screen, trigger string) {
	t.mutex.Lock()
	held := t.screens[player]
	t.mutex.Unlock()
	if held == nil {
		fmt.Fprintf(os.Stderr, "%s; asked Player %s's screen for nothing, because no Receiver session of the Player stands\n", trigger, player)
		return
	}
	held.askScreen(screen, trigger)
}

// askScreen publishes an ask for the Player's screen on the session's
// power topic, not retained, because an ask is an event and not a
// state. A session with no power topic has no screen client that reads
// one.
func (s *session) askScreen(screen, trigger string) {
	action, known := screenActions[screen]
	if !known {
		return
	}
	if s.spec.PowerTopic == "" {
		s.log.printf("%s; asked the screen for nothing, because Player %s's session has no power topic", trigger, s.spec.Player)
		return
	}
	payload, err := json.Marshal(powerAsk{Action: action})
	if err != nil {
		return
	}
	s.bus.Publish(s.spec.PowerTopic, payload, false)
	s.log.printf("%s; published %s to %s", trigger, payload, s.spec.PowerTopic)
}

// powerAsk is the payload the power topic carries both ways.
type powerAsk struct {
	Action string `json:"action"`
}
