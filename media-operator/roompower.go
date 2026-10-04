package main

// The room's power. A power press on a unit whose screen is wired
// through a Receiver publishes an ask on the Player's power topic: the
// idle screen client publishes it between films, and the client under
// a film publishes it once the film ends (power.go). The ask is toggle,
// or on and off for a TV remote's deterministic power functions
// (HDMI-CEC 1.3a, CEC 13.13.3). This operator reads the topic and
// writes each ask into the session's powerAsk. The equipment operator
// reads the new time and turns the room off or on from the room it
// reads.
//
// The same topic carries wake and sleep the other way, for the Player's
// screen (screenask.go). This operator publishes those itself, so it
// reads them back and writes nothing for them.

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// The power asks a screen client publishes.
var roomPowerActions = map[string]bool{"toggle": true, "on": true, "off": true}

// powerMessage is the payload the power topic carries both ways.
type powerMessage struct {
	Action string `json:"action"`
}

// askPower writes one power ask from the unit's power topic on the
// unit's Receiver. A unit with no Receiver has no room to turn, and the
// line says so, because a person pressed a key.
func (o *operator) askPower(namespace, name string, payload []byte) {
	var message powerMessage
	if err := json.Unmarshal(payload, &message); err != nil || !roomPowerActions[message.Action] {
		return
	}
	unit := playerKey(namespace, name)
	topic := playerPowerTopic(o.topicBase, namespace, name)
	receiver, held := o.ensure.receiverFor(unit)
	if !held {
		logLine(o.log, "player %s: %s on %s, wrote nothing, because the unit matches no receiver input", unit, message.Action, topic)
		return
	}
	err := o.sessions.ask(o.client, receiver.name, func(asks *receiverAsks) {
		asks.power = &ReceiverAction{Action: message.Action, At: nextAskTime(previousAt(asks.power), time.Now())}
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "player %s: %s on %s, writing powerAsk on receiver %s: %v\n",
			unit, message.Action, topic, receiver.name, err)
		return
	}
	logLine(o.log, "player %s: %s on %s, wrote powerAsk %s on receiver %s",
		unit, message.Action, topic, message.Action, receiver.name)
}
