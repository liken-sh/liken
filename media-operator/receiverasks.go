package main

// A Receiver's status.session holds a Player's session and three
// requests to equipment-operator: volumeAsk, powerAsk, and inputAsk.
// This operator writes the block by server-side apply under one field
// manager, and an apply removes each field that its manager owned and
// the new body omits. So every write from sessionWriter sends the whole
// block: the session that the pass last applied and the newest request
// of each kind.
//
// The pass, the bus reader, and the repeat timer of a held volume key
// all write the block. equipment-operator acts once on each new value of
// a request's at field, so a block applied out of order would make it
// repeat an older request. One mutex covers composing and sending, so
// the writes reach the API server in the order they were composed.

import (
	"errors"
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// VolumeAsk requests a volume level, in the receiver's own scale, and
// a mute state.
type VolumeAsk struct {
	Level float64 `json:"level"`
	Mute  bool    `json:"mute"`
	At    string  `json:"at"`
}

// ReceiverAction is a power or input request. equipment-operator
// accepts the power actions toggle, on, and off, and this operator
// writes only off, when a Player turns off (playerpower.go).
type ReceiverAction struct {
	Action string `json:"action"`
	At     string `json:"at"`
}

// The input actions. ensure selects the Player's input on the receiver.
// show also asks the room's TV to show the Player's Display, for a home
// key. Neither action turns on a TV or a receiver that is off.
const (
	inputEnsure = "ensure"
	inputShow   = "show"
)

// askTimeLayout is RFC 3339 with milliseconds, so two requests within
// one second have different at values.
const askTimeLayout = "2006-01-02T15:04:05.000Z07:00"

// nextAskTime returns the at value for a new request. When the time
// equals previous, as for two requests in one millisecond, it returns
// previous plus one millisecond, so equipment-operator does not ignore
// the second request as a repeat.
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

// errNoSession is returned for a request on a Receiver that has no
// session from this operator, because applying the request alone would
// write a session with no Player.
var errNoSession = errors.New("the Receiver has no session of this operator")

// sessionWriter sends every write of status.session, and caches the
// session and the requests that it last sent to each Receiver. The zero
// value is ready to use.
type sessionWriter struct {
	mutex sync.Mutex
	// sessions has the session of each Receiver, without its requests.
	sessions map[string]ReceiverSession
	asks     map[string]receiverAsks
}

// receiverAsks is the newest request of each kind for one Receiver.
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

// apply writes a new session to one Receiver, with its cached
// requests.
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

// adopt caches a session that an earlier run of this operator wrote to
// the Receiver, so a request can be applied with it without writing the
// session again.
func (w *sessionWriter) adopt(receiver string, session ReceiverSession) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.init()
	w.sessions[receiver] = session.base()
}

// lift removes the whole session block, requests included, from one
// Receiver.
func (w *sessionWriter) lift(client *apiclient.Client, receiver string) error {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.init()
	// A deleted Receiver has no block to remove, so the cache entry is
	// deleted on ErrNotFound too, and the error is still returned.
	err := ApplyReceiverSession(client, receiver, nil)
	if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
		return err
	}
	delete(w.sessions, receiver)
	delete(w.asks, receiver)
	return err
}

// ask applies one request to a Receiver, with the session and the
// other cached requests. change sets the request in the cache. It runs
// under the writer's mutex, so it can read the at value of the
// previous request.
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

func composeSession(session ReceiverSession, asks receiverAsks) ReceiverSession {
	session.VolumeAsk = asks.volume
	session.PowerAsk = asks.power
	session.InputAsk = asks.input
	return session
}

// previousAt returns the at value of a cached request, or an empty
// string when there is none.
func previousAt(action *ReceiverAction) string {
	if action == nil {
		return ""
	}
	return action.At
}
