package main

// When a TV sends a CEC message that should wake or sleep a Player's
// screen, such as a Set Stream Path to a sleeping Player's Display,
// equipment-operator's CEC node pod writes the request to the
// Television's status.screenAsk. That pod has no connection to the MQTT
// broker, so this operator reads the Televisions and publishes each new
// request on the Player's power topic. media-screen, in the Player's
// pods, subscribes to that topic: wake turns the screen and its panel
// on, and sleep turns them off.

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Television and Receiver are both cluster-scoped kinds of
// equipment-operator's API group.
const televisionAPIVersion = receiverAPIVersion

// Television declares only the status field this operator reads.
type Television struct {
	Metadata ObjectMeta       `json:"metadata"`
	Status   TelevisionStatus `json:"status"`
}

type TelevisionStatus struct {
	ScreenAsk *TelevisionScreenAsk `json:"screenAsk,omitempty"`
}

// TelevisionScreenAsk is one request for a Player's screen. Each new
// request has a new At. Player is namespace/name, and Screen is Wake or
// Sleep.
type TelevisionScreenAsk struct {
	At     string `json:"at"`
	Player string `json:"player"`
	Screen string `json:"screen"`
	Cause  string `json:"cause,omitempty"`
}

const (
	screenWake  = "wake"
	screenSleep = "sleep"
)

var screenActions = map[string]string{
	"Wake":  screenWake,
	"Sleep": screenSleep,
}

type powerMessage struct {
	Action string `json:"action"`
}

// seenScreenRequests maps each Television to the last request read from it, so
// that each request is published once. Only the reconcile goroutine uses
// it.
type screenRequestLog struct {
	listed bool
	seen   map[string]TelevisionScreenAsk
}

// relayScreenAsks publishes each request that differs from the last one
// read from its Television. The first read only records the requests,
// because they were written before this operator started and the screen
// may have changed since.
func (o *operator) relayScreenAsks() {
	televisions, err := o.view.Televisions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the Television objects: %v\n", err)
		return
	}
	first := !o.seenScreenRequests.listed
	o.seenScreenRequests.listed = true
	if o.seenScreenRequests.seen == nil {
		o.seenScreenRequests.seen = map[string]TelevisionScreenAsk{}
	}
	for _, television := range televisions {
		ask := television.Status.ScreenAsk
		if ask == nil {
			continue
		}
		name := television.Metadata.Name
		if last, seen := o.seenScreenRequests.seen[name]; seen && last == *ask {
			continue
		}
		o.seenScreenRequests.seen[name] = *ask
		if !first {
			o.relayScreenAsk(name, *ask)
		}
	}
}

// relayScreenAsk publishes one request on its Player's power topic. The
// message is not retained, so a client that subscribes later does not
// act on an old request. equipment-operator writes a request only for a
// Player that has a session on the TV, and it opens that session from a
// Receiver's session. So when the Player has no Receiver now, the
// request is late, and it must not turn the screen on or off.
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
	if _, wired := o.playerReceivers.receiverFor(playerKey(namespace, name)); !wired {
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
