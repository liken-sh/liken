package main

// The asks a unit makes of its Receiver, and the one path every write
// of status.session takes.
//
// The session block holds the unit's session and its three asks:
// volumeAsk, powerAsk, and inputAsk. This operator writes the block by
// server-side apply under its own field manager, and an apply removes
// each field the manager owned and did not state again. So an apply of
// the session alone would remove the asks, and an apply of one ask
// would remove the session. Every write composes the whole block: the
// session the pass last applied, and the newest ask of each kind.
//
// The pass and the bus reader both write the block, and a held volume
// key writes it from a timer too. One mutex covers the compose and the
// send, so the writes reach the API server in the order they were
// composed, and an older block never lands over a newer one. Each ask
// is one new time in its at field, and the equipment operator acts on
// a new time once, so a block that landed out of order would send the
// receiver an earlier ask again.

import (
	"errors"
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// VolumeAsk is a level and a mute for a device, in the device's own
// units, and the time the ask was made.
type VolumeAsk struct {
	Level float64 `json:"level"`
	Mute  bool    `json:"mute"`
	At    string  `json:"at"`
}

// ReceiverAction is a power ask or an input ask: the action, and the
// time the ask was made. A power ask is toggle, on, or off. An input
// ask is ensure or show.
type ReceiverAction struct {
	Action string `json:"action"`
	At     string `json:"at"`
}

// The input asks. ensure asks the receiver for the unit's input. show
// asks for the input and for the room's TV on the unit's input when
// the TV is on.
const (
	inputEnsure = "ensure"
	inputShow   = "show"
)

// askTimeLayout is RFC 3339 with milliseconds. Each ask carries its
// time, and each new time is one ask, so two asks in one second need
// the milliseconds to differ.
const askTimeLayout = "2006-01-02T15:04:05.000Z07:00"

// nextAskTime is the time of a new ask. Two asks of one kind in the
// same millisecond would carry one time, and the equipment operator
// would read the second as the first, so the second moves one
// millisecond past the first.
func nextAskTime(previous string, now time.Time) string {
	at := now.UTC().Format(askTimeLayout)
	if at != previous {
		return at
	}
	before, err := time.Parse(askTimeLayout, previous)
	if err != nil {
		return at
	}
	return before.Add(time.Millisecond).UTC().Format(askTimeLayout)
}

// errNoSession is the answer to an ask for a Receiver that holds no
// session of this operator's. The ask has no unit to belong to, and an
// apply of the ask alone would write a session with no Player.
var errNoSession = errors.New("the receiver holds no session of this operator's")

// sessionWriter holds the block this operator last composed for each
// Receiver. The zero value is ready to use.
type sessionWriter struct {
	mutex sync.Mutex
	// sessions is the session the pass applied or adopted on each
	// Receiver, with no asks.
	sessions map[string]ReceiverSession
	asks     map[string]receiverAsks
}

// receiverAsks is the newest ask of each kind for one Receiver.
type receiverAsks struct {
	volume *VolumeAsk
	power  *ReceiverAction
	input  *ReceiverAction
}

func (w *sessionWriter) init() {
	if w.sessions == nil {
		w.sessions = map[string]ReceiverSession{}
		w.asks = map[string]receiverAsks{}
	}
}

// apply writes a new session on one Receiver, with the asks it holds.
func (w *sessionWriter) apply(client *apiclient.Client, receiver string, session ReceiverSession) error {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.init()
	composed := composeSession(session.base(), w.asks[receiver])
	if err := ApplyReceiverSession(client, receiver, &composed); err != nil {
		return err
	}
	w.sessions[receiver] = session.base()
	return nil
}

// adopt records a session the Receiver already holds from an earlier
// run of this operator, so an ask can be applied beside it with no
// write of the session.
func (w *sessionWriter) adopt(receiver string, session ReceiverSession) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.init()
	w.sessions[receiver] = session.base()
}

// lift removes the whole block from one Receiver, its asks with it.
func (w *sessionWriter) lift(client *apiclient.Client, receiver string) error {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.init()
	// A Receiver that is gone holds no block, so its record goes too.
	err := ApplyReceiverSession(client, receiver, nil)
	if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
		return err
	}
	delete(w.sessions, receiver)
	delete(w.asks, receiver)
	return err
}

// ask records one ask on a Receiver and applies the block that carries
// it. change sets the ask on the record. It reads the record under the
// writer's mutex, so it can read the previous ask's time.
func (w *sessionWriter) ask(client *apiclient.Client, receiver string, change func(*receiverAsks)) error {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.init()
	session, held := w.sessions[receiver]
	if !held {
		return errNoSession
	}
	asks := w.asks[receiver]
	change(&asks)
	w.asks[receiver] = asks
	composed := composeSession(session, asks)
	return ApplyReceiverSession(client, receiver, &composed)
}

// composeSession lays the asks over a session.
func composeSession(session ReceiverSession, asks receiverAsks) ReceiverSession {
	session.VolumeAsk = asks.volume
	session.PowerAsk = asks.power
	session.InputAsk = asks.input
	return session
}

// previousAt reads the time of an ask the record holds, empty for none.
func previousAt(action *ReceiverAction) string {
	if action == nil {
		return ""
	}
	return action.At
}
