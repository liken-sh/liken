package main

// The relay of an ask for a Player's screen. The equipment operator's
// CEC node workload hears the TV ask for a screen, such as a Set Stream
// Path for a sleeping Player's Display, and writes the ask in the
// Television's status.screenAsk. The node workload holds no broker
// connection. This operator watches the Televisions and publishes each
// new ask on the Player's power topic, where the Player's screen client
// reads it: wake lifts the screen and asks for its panel, and sleep
// lowers it.

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// The equipment operator's group serves the Television too. A
// Television is cluster-scoped, like a Receiver.
const televisionAPIVersion = receiverAPIVersion

// A Television carries only the ask this operator relays.
type Television struct {
	Metadata ObjectMeta       `json:"metadata"`
	Status   TelevisionStatus `json:"status"`
}

type TelevisionStatus struct {
	ScreenAsk *TelevisionScreenAsk `json:"screenAsk,omitempty"`
}

// TelevisionScreenAsk is one ask for a Player's screen. Each new time in
// At is one ask. Player is namespace/name, and Screen is Wake or Sleep.
type TelevisionScreenAsk struct {
	At     string `json:"at"`
	Player string `json:"player"`
	Screen string `json:"screen"`
	Cause  string `json:"cause,omitempty"`
}

// The power topic's actions for each screen ask.
var screenActions = map[string]string{
	"Wake":  "wake",
	"Sleep": "sleep",
}

// screenAsks is what the pass holds about the asks it relayed. listed
// says the pass has read the Televisions once, and seen maps each
// Television to the last ask the pass read there. Only the pass
// goroutine touches it.
type screenAsks struct {
	listed bool
	seen   map[string]TelevisionScreenAsk
}

// relayScreenAsks publishes each new ask a Television's status holds,
// once. An ask that the first pass finds is older than this operator's
// start, and a person may have done anything since, so the pass relays
// nothing for it.
func (o *operator) relayScreenAsks() {
	televisions, err := o.view.Televisions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading televisions: %v\n", err)
		return
	}
	first := !o.screenAsks.listed
	o.screenAsks.listed = true
	if o.screenAsks.seen == nil {
		o.screenAsks.seen = map[string]TelevisionScreenAsk{}
	}
	for _, television := range televisions {
		ask := television.Status.ScreenAsk
		if ask == nil {
			continue
		}
		name := television.Metadata.Name
		if held, seen := o.screenAsks.seen[name]; seen && held == *ask {
			continue
		}
		o.screenAsks.seen[name] = *ask
		if !first {
			o.relayScreenAsk(name, *ask)
		}
	}
}

// relayScreenAsk publishes one ask on its Player's power topic, not
// retained, because an ask is an event and not a state. The ask goes
// only to a unit whose screen is wired through a Receiver. Every unit
// has a power topic, and a client in the screen mode handles power
// itself, so a late ask for a unit whose Receiver was removed must not
// wake or lower that screen.
func (o *operator) relayScreenAsk(television string, ask TelevisionScreenAsk) {
	action, known := screenActions[ask.Screen]
	namespace, name, named := strings.Cut(ask.Player, "/")
	if !known || !named || namespace == "" || name == "" {
		return
	}
	cause := ask.Cause
	if cause == "" {
		cause = "screenAsk " + ask.Screen
	}
	if _, held := o.ensure.receiverFor(playerKey(namespace, name)); !held {
		logLine(o.log, "television %s: %s, relayed nothing, because player %s is wired to no receiver",
			television, cause, ask.Player)
		return
	}
	payload, err := json.Marshal(powerMessage{Action: action})
	if err != nil {
		return
	}
	topic := playerPowerTopic(o.topicBase, namespace, name)
	o.bus.Publish(topic, payload, false)
	logLine(o.log, "television %s: %s, published %s to %s", television, cause, payload, topic)
}
