package main

// The media layer's half of the equipment operator's Receiver: the
// match from a unit's screen to a Receiver input, the session this
// operator applies for as long as the unit matches, the active flag on
// that session that follows a standing Play, and the lift when the unit
// stops matching or goes away.
//
// The session also carries an awake flag, which follows the panel
// desire and not the Play, so the power key at the idle screen reaches
// the equipment too.

import (
	"errors"
	"fmt"
	"os"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// The group the equipment operator serves. A Receiver is cluster-
// scoped, because equipment is physical and belongs to no namespace.
const receiverAPIVersion = "equipment.liken.sh/v1alpha1"

// The condition the equipment operator sets from a round trip to the
// equipment. The Player status folds it to one word.
const receiverReachableCondition = "Reachable"

// The condition the equipment operator sets while the receiver reports
// the session's input. An ensure ask goes out only while it is not
// True, so a press on a room that already shows the unit writes
// nothing to the API server.
const receiverInputSelectedCondition = "InputSelected"

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

// The cluster owner states the inputs and the volume scale. The
// equipment operator reads spec.session when the status holds no
// session, so this operator reads it to adopt it, and releases it once
// status.session holds the session.
type ReceiverSpec struct {
	Inputs  []ReceiverInput  `json:"inputs,omitempty"`
	Session *ReceiverSession `json:"session,omitempty"`
	Volume  *ReceiverVolume  `json:"volume,omitempty"`
}

// ReceiverVolume is the receiver's volume scale for a press, in the
// receiver's own units: the loudest level an ask may set, and the
// distance one press moves the level. A Denon counts 0 to 98 in half
// steps.
type ReceiverVolume struct {
	Max  float64 `json:"max,omitempty"`
	Step float64 `json:"step,omitempty"`
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
// it, the input it plays through, and the asks the unit made.
//
// Active says whether a Play stands on the unit. The session itself
// stands at the idle screen too, so a volume press moves the room while
// nothing plays, and the equipment operator reads Active to tell a
// playing room from an idle one.
//
// Awake says whether the unit's panel is up. It follows the panel
// desire the idle client publishes: the off desire is a dark room, and
// the on desire is a room that is awake. A unit with no desire yet
// keeps the flag its session already carries.
//
// The three asks are events. The equipment operator acts on an ask
// once for each new time in its at field, and it does not act on an
// ask it finds in its first pass after a start (receiverasks.go).
//
// VolumeTopic and PowerTopic are read and never written. A session
// that still names either topic is applied again without it, so the
// equipment operator stops reading the bus for that unit.
type ReceiverSession struct {
	Player      string          `json:"player,omitempty"`
	Input       string          `json:"input,omitempty"`
	Active      bool            `json:"active"`
	Awake       bool            `json:"awake"`
	VolumeAsk   *VolumeAsk      `json:"volumeAsk,omitempty"`
	PowerAsk    *ReceiverAction `json:"powerAsk,omitempty"`
	InputAsk    *ReceiverAction `json:"inputAsk,omitempty"`
	VolumeTopic string          `json:"volumeTopic,omitempty"`
	PowerTopic  string          `json:"powerTopic,omitempty"`
}

// base is the part of a session the pass decides: the unit, the
// input, and the two flags. Two sessions with the same base and no
// topics are the same session to the pass, whatever asks they carry.
func (s ReceiverSession) base() ReceiverSession {
	return ReceiverSession{Player: s.Player, Input: s.Input, Active: s.Active, Awake: s.Awake}
}

// The session is this operator's block of the status. Every other
// field is the equipment operator's.
type ReceiverStatus struct {
	Power      string                  `json:"power,omitempty"`
	Input      string                  `json:"input,omitempty"`
	Driver     string                  `json:"driver,omitempty"`
	Zones      map[string]ReceiverZone `json:"zones,omitempty"`
	Session    *ReceiverSession        `json:"session,omitempty"`
	Conditions []ReceiverCondition     `json:"conditions,omitempty"`
}

// ReceiverZone is what one zone of the receiver last reported. The
// volume is a string in the receiver's own scale, such as 45.5.
type ReceiverZone struct {
	Volume string `json:"volume,omitempty"`
	Mute   bool   `json:"mute,omitempty"`
}

type ReceiverCondition struct {
	Type   string `json:"type,omitempty"`
	Status string `json:"status,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// The body of a session apply carries the status alone, and the
// session alone inside it. The rest of the status is the equipment
// operator's, so this manager never touches it.
type receiverStatusApply struct {
	APIVersion string                `json:"apiVersion"`
	Kind       string                `json:"kind"`
	Metadata   ObjectMeta            `json:"metadata"`
	Status     receiverSessionStatus `json:"status"`
}

type receiverSessionStatus struct {
	Session *ReceiverSession `json:"session,omitempty"`
}

// The body of the spec release carries an empty spec. The inputs are
// the cluster owner's, and an apply that states no field releases only
// the fields this manager owned.
type receiverSpecApply struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	Metadata   ObjectMeta   `json:"metadata"`
	Spec       ReceiverSpec `json:"spec"`
}

// standingSession is the session the equipment operator acts on: the
// one in status, or the one in spec when the status holds none. An
// equipment operator reads the two in that order.
func standingSession(receiver *Receiver) *ReceiverSession {
	if receiver.Status.Session != nil {
		return receiver.Status.Session
	}
	return receiver.Spec.Session
}

// receivers reads the cluster's Receivers from the view at most once a
// pass, and only for a unit whose screen resolved. A cluster that runs
// no equipment operator has no Receivers in the view. failed records a
// read that failed, which matches no unit but says nothing about the
// wiring.
type receivers struct {
	view   *clusterView
	items  []Receiver
	listed bool
	failed bool
}

func newReceivers(view *clusterView) *receivers {
	return &receivers{view: view}
}

// receiverLookup is the pass's one receivers. It is built on first use
// and dropped when the pass ends.
func (o *operator) receiverLookup() *receivers {
	if o.receiverCache == nil {
		o.receiverCache = newReceivers(o.view)
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
		items, err := r.view.Receivers()
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading receivers: %v\n", err)
			r.failed = true
			return nil, "", false
		}
		r.items = items
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
	return receiverCondition(receiver, receiverReachableCondition)
}

// receiverCondition reads one condition's status word, empty for a
// Receiver that carries no such condition.
func receiverCondition(receiver *Receiver, kind string) string {
	for _, condition := range receiver.Status.Conditions {
		if condition.Type == kind {
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
// applies the session on every pass the unit matches an input. The
// session is active only while a Play stands on the unit.
//
// The awake flag comes from the panel desk on every pass, so a wake or
// sleep press at the idle screen reaches the equipment as the same
// session with one flag changed.
func (o *operator) reconcileReceiver(player *Player, standing bool) *PlayerReceiverStatus {
	receiver, input, matched := o.matchReceiver(player)
	key := playerKey(player.Metadata.Namespace, player.Metadata.Name)
	if !matched {
		// A failed read of the Receivers says nothing about the wiring,
		// so the unit keeps the entry the last pass left on the ensure
		// desk. Recording it as unwired would state the screen power mode
		// for every unit and drop a Television's ask for one pass.
		if !o.receiverLookup().failed {
			o.ensure.set(key, unitReceiver{})
		}
		return nil
	}
	o.applySession(player, receiver, input, standing, o.awake(player, receiver))
	// A unit that matches a receiver keeps the receiver on the ensure
	// desk, so a press on its controller asks that receiver for the
	// unit's input, and a power ask on its power topic reaches it.
	o.ensure.set(key, unitReceiver{
		name:          receiver.Metadata.Name,
		inputSelected: receiverCondition(receiver, receiverInputSelectedCondition) == conditionTrue,
	})
	return &PlayerReceiverStatus{
		Name:      receiver.Metadata.Name,
		Input:     input,
		Reachable: receiverReachable(receiver),
	}
}

// awake reads the awake flag from the panel desire. A unit with no
// desire yet keeps the flag its session on this Receiver already
// carries, because no desire is not a statement that the room is lit:
// an operator that starts before the bus delivers the desire would
// otherwise power the equipment on in a dark room. A unit with no
// desire and no session there is awake, the flag a new session starts
// with.
func (o *operator) awake(player *Player, receiver *Receiver) bool {
	switch o.panels.stateFor(playerKey(player.Metadata.Namespace, player.Metadata.Name)) {
	case panelDesireOff:
		return false
	case "":
		held := standingSession(receiver)
		if held != nil && held.Player == player.Metadata.Namespace+"/"+player.Metadata.Name {
			return held.Awake
		}
	}
	return true
}

// matchReceiver resolves the unit's screen and finds the input it is
// wired to. Both reads come from the caches this pass already holds.
// The screen holds through a short gap in the claim's allocation, and
// screengap.go says why.
func (o *operator) matchReceiver(player *Player) (*Receiver, string, bool) {
	found, resolved := o.matchedScreen(player)
	if !resolved {
		return nil, "", false
	}
	return o.receiverLookup().matchFor(found.node, found.monitor)
}

// applyReceiverSession applies the session before the playback pod is
// created, so the equipment is on and on the right input by the time
// mpv draws.
//
// A Play that starts wakes the room, so the creating pass applies an
// awake session, and the next pass reads the panel desire.
func (o *operator) applyReceiverSession(player *Player) {
	receiver, input, matched := o.matchReceiver(player)
	if !matched {
		return
	}
	o.applySession(player, receiver, input, true, true)
}

// applySession writes status.session under this operator's field
// manager, and only when the session differs from the one this operator
// last applied. Power and input are one-shots the equipment answers
// once, so an unchanged session is not sent again. A unit that moved to
// another Receiver lifts the session on the old one first, so no
// equipment keeps a session nothing holds.
//
// The tracking compares the whole session, so a Play that starts or
// ends changes only the active flag, and the session is applied again
// for that.
//
// A panel a person turns off or on changes only the awake flag, and the
// session is applied again for that too.
func (o *operator) applySession(player *Player, receiver *Receiver, input string, active, awake bool) {
	namespace, name := player.Metadata.Namespace, player.Metadata.Name
	key := playerKey(namespace, name)
	session := ReceiverSession{
		Player: namespace + "/" + name,
		Input:  input,
		Active: active,
		Awake:  awake,
	}
	held, tracked := o.receiverSessions[key]
	standing := standingSession(receiver)
	statusHolds := receiver.Status.Session != nil
	switch {
	case tracked && held.receiver == receiver.Metadata.Name && held.session == session:
		// The session this run applied still stands, so nothing is sent.
	case !tracked && sameSession(standing, session) && statusHolds:
		// A session the Receiver already carries is the one an earlier
		// run of this operator applied. The equipment acts on power and
		// input once, so the same session is recorded and not sent again.
		o.receiverSessions[key] = receiverSession{receiver: receiver.Metadata.Name, session: session}
		o.sessions.adopt(receiver.Metadata.Name, session)
	case !tracked && sameSession(standing, session):
		// The same session, held in spec by an earlier build of this
		// operator. It moves to the status once, so the old spec field can
		// be released and later dropped from the schema. The equipment
		// operator reads a session that moves unchanged from spec to status
		// as the same session, so the move sends the receiver nothing.
		if !o.writeSession(key, receiver, session, held, tracked) {
			return
		}
		statusHolds = true
	default:
		if !o.writeSession(key, receiver, session, held, tracked) {
			return
		}
		statusHolds = true
	}
	o.releaseSpecSession(key, receiver, statusHolds)
}

// sameSession answers whether a session the Receiver holds is the one
// this operator would apply. A held session that still names a topic
// is not, so the apply that follows removes the topic.
func sameSession(standing *ReceiverSession, session ReceiverSession) bool {
	if standing == nil || standing.VolumeTopic != "" || standing.PowerTopic != "" {
		return false
	}
	return standing.base() == session
}

// writeSession lifts the session a moved unit left on its old Receiver,
// then applies the new one. It answers whether the new session landed.
func (o *operator) writeSession(key string, receiver *Receiver, session ReceiverSession, held receiverSession, tracked bool) bool {
	if tracked && held.receiver != receiver.Metadata.Name {
		if !o.liftSession(held.receiver) {
			return false
		}
		delete(o.receiverSessions, key)
		logLine(o.log, "player %s: lifted the session on receiver %s, because the unit moved to receiver %s",
			key, held.receiver, receiver.Metadata.Name)
	}
	if err := o.sessions.apply(o.client, receiver.Metadata.Name, session); err != nil {
		fmt.Fprintf(os.Stderr, "applying the session on receiver %s: %v\n",
			receiver.Metadata.Name, err)
		return false
	}
	o.receiverSessions[key] = receiverSession{receiver: receiver.Metadata.Name, session: session}
	logLine(o.log, "player %s: applied the session on receiver %s: input %s, active %t, awake %t",
		key, receiver.Metadata.Name, session.Input, session.Active, session.Awake)
	return true
}

// releaseSpecSession removes a session this operator applied to
// spec.session, once status.session holds the session. The order
// matters: an equipment operator falls back to spec.session when the
// status holds none, so a release before the status write would end the
// session for a moment.
//
// The release is sent at most once a run for each Receiver. The API
// server removes only a field this manager owns, so a spec.session
// another manager wrote stays, and a second apply would change nothing.
func (o *operator) releaseSpecSession(key string, receiver *Receiver, statusHolds bool) {
	name := receiver.Metadata.Name
	if receiver.Spec.Session == nil {
		o.specReleased[name] = true
		return
	}
	if !statusHolds || o.specReleased[name] {
		return
	}
	if err := ReleaseReceiverSpecSession(o.client, name); err != nil {
		fmt.Fprintf(os.Stderr, "releasing spec.session on receiver %s: %v\n", name, err)
		return
	}
	o.specReleased[name] = true
	logLine(o.log, "player %s: released spec.session on receiver %s, because status.session holds the session",
		key, name)
}

// liftSession removes this operator's session from one Receiver: the
// spec.session a run that wrote spec left there, then the status. A
// Receiver that is gone carries no session, so the lift it refuses has
// already landed. It answers whether the lift landed.
func (o *operator) liftSession(name string) bool {
	if !o.specReleased[name] {
		err := ReleaseReceiverSpecSession(o.client, name)
		if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "releasing spec.session on receiver %s: %v\n", name, err)
			return false
		}
		o.specReleased[name] = true
	}
	err := o.sessions.lift(o.client, name)
	if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "lifting the session on receiver %s: %v\n", name, err)
		return false
	}
	return true
}

// retainSessions lifts the session of every unit that no longer matches
// a Receiver input, the way retainPanels lifts an override. A Play that
// ends is no lift: the session stands and its active flag goes false. A
// lift that fails keeps the entry, so the next pass writes it again.
func (o *operator) retainSessions(matched map[string]bool) {
	for key, held := range o.receiverSessions {
		if matched[key] {
			continue
		}
		if !o.liftSession(held.receiver) {
			continue
		}
		delete(o.receiverSessions, key)
		logLine(o.log, "player %s: lifted the session on receiver %s, because the unit matches no receiver input now",
			key, held.receiver)
	}
}

// A unit holds a standing run while a Play names it and that Play has
// not finished. This is the same condition that keeps its playback pod:
// a Play created this pass carries no phase yet, and a failed Play
// keeps its pod until it is recreated.
//
// A Pending-or-Running test would report the room idle while its film
// is still up, which is why the test is the unfinished phase.
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
