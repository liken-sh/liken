package main

// The media layer's half of the equipment operator's Receiver: the
// match from a unit's screen to a Receiver input, the session this
// operator applies while a Play stands, and the lift when the Play
// ends.

import (
	"errors"
	"fmt"
	"os"
)

// The group the equipment operator serves. A Receiver is cluster-
// scoped, because equipment is physical and belongs to no namespace.
const receiverAPIVersion = "equipment.liken.sh/v1alpha1"

// The condition the equipment operator sets from a round trip to the
// equipment. The Player status folds it to one word.
const receiverReachableCondition = "Reachable"

// A Receiver carries only what this operator reads or writes: the
// wiring it matches on, the session it applies, and the observed values
// it folds into the Player's status.
type Receiver struct {
	APIVersion string         `json:"apiVersion,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	Metadata   ObjectMeta     `json:"metadata"`
	Spec       ReceiverSpec   `json:"spec"`
	Status     ReceiverStatus `json:"status"`
}

type ReceiverList struct {
	Metadata ListMeta   `json:"metadata"`
	Items    []Receiver `json:"items"`
}

// The cluster owner states the inputs. The session is this operator's
// block.
type ReceiverSpec struct {
	Inputs  []ReceiverInput  `json:"inputs,omitempty"`
	Session *ReceiverSession `json:"session,omitempty"`
}

// One input of the equipment, and the machine and monitor id wired into
// it. The monitor id alone names no input, because one receiver
// forwards one EDID on every input.
type ReceiverInput struct {
	Name    string `json:"name,omitempty"`
	Machine string `json:"machine,omitempty"`
	Monitor string `json:"monitor,omitempty"`
}

// The session one Player holds on the equipment: the unit that holds
// it, the input it plays through, and the topic the level comes from.
type ReceiverSession struct {
	Player      string `json:"player,omitempty"`
	Input       string `json:"input,omitempty"`
	VolumeTopic string `json:"volumeTopic,omitempty"`
}

type ReceiverStatus struct {
	Power      string              `json:"power,omitempty"`
	Input      string              `json:"input,omitempty"`
	Conditions []ReceiverCondition `json:"conditions,omitempty"`
}

type ReceiverCondition struct {
	Type   string `json:"type,omitempty"`
	Status string `json:"status,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// The body of an apply carries the spec alone, and the session alone
// inside it. The inputs are the cluster owner's and the status is the
// equipment operator's, so this manager never touches either.
type receiverApply struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	Metadata   ObjectMeta   `json:"metadata"`
	Spec       ReceiverSpec `json:"spec"`
}

// receivers lists the cluster's Receivers at most once a pass, and only
// for a unit whose screen resolved. A cluster that runs no equipment
// operator makes no request at all.
type receivers struct {
	client *Client
	items  []Receiver
	listed bool
}

func newReceivers(client *Client) *receivers {
	return &receivers{client: client}
}

// receiverLookup is the pass's one receivers. It is built on first use
// and dropped when the pass ends.
func (o *operator) receiverLookup() *receivers {
	if o.receiverCache == nil {
		o.receiverCache = newReceivers(o.client)
	}
	return o.receiverCache
}

// matchFor finds the Receiver and the input name wired to this node and
// monitor id. No match is the normal case: a unit plays straight into a
// panel, or the cluster declares no equipment.
func (r *receivers) matchFor(node, monitor string) (*Receiver, string, bool) {
	if node == "" || monitor == "" {
		return nil, "", false
	}
	if !r.listed {
		r.listed = true
		list, err := ListReceivers(r.client)
		if err != nil {
			if !errors.Is(err, ErrNotFound) {
				fmt.Fprintf(os.Stderr, "listing receivers: %v\n", err)
			}
			return nil, "", false
		}
		r.items = list.Items
	}
	for index := range r.items {
		receiver := &r.items[index]
		if input, matched := matchReceiverInput(receiver, node, monitor); matched {
			return receiver, input, true
		}
	}
	return nil, "", false
}

// An entry matches on both values. The machine names which input
// carries the cable, and the monitor id proves the cable is there.
func matchReceiverInput(receiver *Receiver, node, monitor string) (string, bool) {
	for _, input := range receiver.Spec.Inputs {
		if input.Name == "" || input.Machine != node || input.Monitor != monitor {
			continue
		}
		return input.Name, true
	}
	return "", false
}

// The Reachable condition's status word, empty for a Receiver that
// carries no such condition.
func receiverReachable(receiver *Receiver) string {
	for _, condition := range receiver.Status.Conditions {
		if condition.Type == receiverReachableCondition {
			return condition.Status
		}
	}
	return ""
}

// The session this operator last applied for one unit, and the Receiver
// it applied it to. The name is held because a deleted Player takes the
// path from the unit to its equipment with it, and the lift still needs
// a Receiver to write.
type receiverSession struct {
	receiver string
	session  ReceiverSession
}

// reconcileReceiver builds the Player's receiver status block, and
// applies the session while a Play stands on the unit.
func (o *operator) reconcileReceiver(player *Player, standing bool) *PlayerReceiverStatus {
	receiver, input, matched := o.matchReceiver(player)
	if !matched {
		return nil
	}
	if standing {
		o.applySession(player, receiver, input)
	}
	return &PlayerReceiverStatus{
		Name:      receiver.Metadata.Name,
		Input:     input,
		Reachable: receiverReachable(receiver),
	}
}

// matchReceiver resolves the unit's screen and finds the input it is
// wired to. Both reads come from the caches this pass already holds.
func (o *operator) matchReceiver(player *Player) (*Receiver, string, bool) {
	found, resolved := o.screenLookup().screenFor(player)
	if !resolved {
		return nil, "", false
	}
	return o.receiverLookup().matchFor(found.node, found.monitor)
}

// applyReceiverSession applies the session before the playback pod is
// created, so the equipment is on and on the right input by the time
// mpv draws.
func (o *operator) applyReceiverSession(player *Player) {
	receiver, input, matched := o.matchReceiver(player)
	if !matched {
		return
	}
	o.applySession(player, receiver, input)
}

// applySession writes spec.session under this operator's field manager,
// and only when the session differs from the one this operator last
// applied. Power and input are one-shots the equipment answers once, so
// an unchanged session is not sent again. A unit that moved to another
// Receiver lifts the session on the old one first, so no equipment
// keeps a session nothing holds.
func (o *operator) applySession(player *Player, receiver *Receiver, input string) {
	namespace, name := player.Metadata.Namespace, player.Metadata.Name
	key := playerKey(namespace, name)
	session := ReceiverSession{
		Player:      namespace + "/" + name,
		Input:       input,
		VolumeTopic: playerVolumeTopic(o.topicBase, namespace, name),
	}
	held, tracked := o.receiverSessions[key]
	if tracked && held.receiver == receiver.Metadata.Name && held.session == session {
		return
	}
	if tracked && held.receiver != receiver.Metadata.Name {
		err := ApplyReceiverSession(o.client, held.receiver, nil)
		if err != nil && !errors.Is(err, ErrNotFound) {
			fmt.Fprintf(os.Stderr, "lifting the session on receiver %s: %v\n", held.receiver, err)
			return
		}
		delete(o.receiverSessions, key)
	}
	if err := ApplyReceiverSession(o.client, receiver.Metadata.Name, &session); err != nil {
		fmt.Fprintf(os.Stderr, "applying the session on receiver %s: %v\n",
			receiver.Metadata.Name, err)
		return
	}
	o.receiverSessions[key] = receiverSession{receiver: receiver.Metadata.Name, session: session}
}

// retainSessions lifts the session of every unit that no longer holds a
// standing Play, the way retainPanels lifts an override. A lift that
// fails keeps the entry, so the next pass writes it again.
func (o *operator) retainSessions(standing map[string]bool) {
	for key, held := range o.receiverSessions {
		if standing[key] {
			continue
		}
		err := ApplyReceiverSession(o.client, held.receiver, nil)
		if err != nil && !errors.Is(err, ErrNotFound) {
			fmt.Fprintf(os.Stderr, "lifting the session on receiver %s: %v\n", held.receiver, err)
			continue
		}
		delete(o.receiverSessions, key)
	}
}

// A unit holds a standing run while a Play names it and that Play has
// not finished. This is the same condition that keeps its playback pod:
// a Play created this pass carries no phase yet, and a failed Play
// keeps its pod until it is recreated, so a Pending-or-Running test
// would lift the session too early.
func playerHasStandingPlay(player *Player, plays []Play) bool {
	for index := range plays {
		play := &plays[index]
		if play.Metadata.Namespace != player.Metadata.Namespace {
			continue
		}
		if playerName(play) != player.Metadata.Name {
			continue
		}
		if !finishedPhase(play.Status.Phase) {
			return true
		}
	}
	return false
}
