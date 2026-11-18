package main

// This file is media-operator's side of the Receiver resource that
// equipment-operator serves. While a Player's screen is connected to a
// Receiver input, media-operator writes a session to that Receiver's
// status.session. The session tells equipment-operator which input to
// select, whether a Play is running, and whether the Player's panel
// should be on. When the Player no longer matches an input, or is
// deleted, media-operator removes the session.

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// A Receiver is cluster-scoped, because the equipment is physical and
// belongs to no namespace.
const receiverAPIVersion = "equipment.liken.sh/v1alpha1"

// receiverReachableCondition is the condition that equipment-operator
// sets from a round trip to the equipment. media-operator copies its
// status into the Player's status.
const receiverReachableCondition = "Reachable"

// receiverInputSelectedCondition is the condition that
// equipment-operator sets while the receiver reports the session's input
// as selected. media-operator writes an inputAsk of ensure only while
// the condition is not True, so a button press in a room that already
// shows the Player writes nothing to the API server.
const receiverInputSelectedCondition = "InputSelected"

// Receiver has only the fields that media-operator reads or writes.
type Receiver struct {
	APIVersion string         `json:"apiVersion,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	Metadata   ObjectMeta     `json:"metadata"`
	Spec       ReceiverSpec   `json:"spec"`
	Status     ReceiverStatus `json:"status"`
}

// ReceiverSpec is what the cluster owner declares: the inputs and the
// volume scale. Earlier versions of media-operator wrote the session to
// spec.session, and equipment-operator still reads that field when
// status.session is empty. So media-operator reads spec.session to adopt
// it, and releases it once status.session has the session.
type ReceiverSpec struct {
	Inputs  []ReceiverInput  `json:"inputs,omitempty"`
	Session *ReceiverSession `json:"session,omitempty"`
	Volume  *ReceiverVolume  `json:"volume,omitempty"`
}

// ReceiverVolume is the receiver's volume scale for button presses, in
// the receiver's own units. Max is the loudest level a volume request may
// set, and Step is how far one press moves the level. A Denon counts
// from 0 to 98 in half steps. Indicator selects what draws the volume
// indicator: the Player's screens, or the receiver's own overlay on the
// TV.
type ReceiverVolume struct {
	Max       float64 `json:"max,omitempty"`
	Step      float64 `json:"step,omitempty"`
	Indicator string  `json:"indicator,omitempty"`
}

// receiverIndicatorReceiver is the spec.volume.indicator value for a
// receiver that draws its own volume overlay on the TV. The CRD defaults
// the field to Player, and media-operator treats an absent field the
// same way.
const receiverIndicatorReceiver = "Receiver"

// ReceiverInput is one input of the equipment, with the machine and
// the monitor id that are connected to it. The monitor id alone does
// not identify an input, because a receiver forwards the same EDID on
// every input.
type ReceiverInput struct {
	Name    string `json:"name,omitempty"`
	Machine string `json:"machine,omitempty"`
	Monitor string `json:"monitor,omitempty"`
}

// ReceiverSession is what one Player writes to a Receiver: the Player,
// the input it plays through, two flags, and its requests: volumeAsk,
// powerAsk, and inputAsk.
//
// Active is true while a Play is running on the Player. The session
// exists while the Player shows its idle screen too, so a volume press
// changes the room's volume when nothing plays.
//
// Awake is true while the Player's panel should be on. awake explains
// how media-operator computes it.
//
// Each request is a one-time action, not state. equipment-operator sends
// a request to the receiver once for each new value of its At field. On its
// first pass after it starts, it records each At without sending
// (receiverasks.go).
//
// VolumeTopic and PowerTopic are read and never written. When a session
// still has either one, media-operator applies the session again without
// it, so equipment-operator stops reading the bus for that Player.
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

// base returns the fields of a session that the reconcile pass sets.
// Two sessions with the same base and no topics count as the same
// session, whatever requests they have.
func (s ReceiverSession) base() ReceiverSession {
	return ReceiverSession{Player: s.Player, Input: s.Input, Active: s.Active, Awake: s.Awake}
}

// media-operator writes Session, and equipment-operator writes the
// other fields of ReceiverStatus.
type ReceiverStatus struct {
	Power      string                  `json:"power,omitempty"`
	Input      string                  `json:"input,omitempty"`
	Driver     string                  `json:"driver,omitempty"`
	Zones      map[string]ReceiverZone `json:"zones,omitempty"`
	Session    *ReceiverSession        `json:"session,omitempty"`
	Conditions []ReceiverCondition     `json:"conditions,omitempty"`
}

// Volume is a string in the receiver's own scale, such as 45.5.
type ReceiverZone struct {
	Volume string `json:"volume,omitempty"`
	Mute   bool   `json:"mute,omitempty"`
}

type ReceiverCondition struct {
	Type   string `json:"type,omitempty"`
	Status string `json:"status,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// receiverStatusApply is the body of a server-side apply of the
// session. It sets only status.session, so media-operator's field
// manager owns no other field of the status.
type receiverStatusApply struct {
	APIVersion string                `json:"apiVersion"`
	Kind       string                `json:"kind"`
	Metadata   ObjectMeta            `json:"metadata"`
	Status     receiverSessionStatus `json:"status"`
}

type receiverSessionStatus struct {
	Session *ReceiverSession `json:"session,omitempty"`
}

// receiverSpecApply is the body of the apply that releases
// spec.session. It sets no spec field. A server-side apply removes only
// the fields that the same field manager owned before, so the cluster
// owner's inputs stay.
type receiverSpecApply struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	Metadata   ObjectMeta   `json:"metadata"`
	Spec       ReceiverSpec `json:"spec"`
}

// currentSession returns the session equipment-operator acts on:
// status.session, or spec.session when status.session is empty.
func currentSession(receiver *Receiver) *ReceiverSession {
	if receiver.Status.Session != nil {
		return receiver.Status.Session
	}
	return receiver.Spec.Session
}

// receivers lists the cluster's Receivers from the view at most once
// per pass, and only when a Player's screen has resolved. failed
// records that the list returned an error. A failed list matches no
// Player, but it does not show that the wiring changed.
type receivers struct {
	view   *clusterView
	items  []Receiver
	listed bool
	failed bool
}

func newReceivers(view *clusterView) *receivers {
	return &receivers{view: view}
}

// receiverLookup returns the receivers cache of the current pass. The
// pass clears the cache when it ends.
func (o *operator) receiverLookup() *receivers {
	if o.receiverCache == nil {
		o.receiverCache = newReceivers(o.view)
	}
	return o.receiverCache
}

// matchFor returns the Receiver and input name connected to this node
// and monitor id. Finding none is normal: a Player may be connected
// straight to a panel, or the cluster may run no equipment-operator.
func (r *receivers) matchFor(node, monitor string) (*Receiver, string, bool) {
	if node == "" || monitor == "" {
		return nil, "", false
	}
	if !r.listed {
		r.listed = true
		items, err := r.view.Receivers()
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading the Receivers: %v\n", err)
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

// matchReceiverInput returns the name of the input whose machine and
// monitor id both match. ReceiverInput says why both must match.
func matchReceiverInput(receiver *Receiver, node, monitor string) (string, bool) {
	for _, input := range receiver.Spec.Inputs {
		if input.Name == "" || input.Machine != node || input.Monitor != monitor {
			continue
		}
		return input.Name, true
	}
	return "", false
}

func receiverReachable(receiver *Receiver) string {
	return receiverCondition(receiver, receiverReachableCondition)
}

// receiverCondition returns the status of the condition of the given
// type, or an empty string when the Receiver has no such condition.
func receiverCondition(receiver *Receiver, kind string) string {
	for _, condition := range receiver.Status.Conditions {
		if condition.Type == kind {
			return condition.Status
		}
	}
	return ""
}

// receiverSession is the session media-operator last applied for one
// Player, and the Receiver it applied it to. The Receiver name is
// stored because a deleted Player's screen matches no Receiver, and
// removing the session needs the Receiver to write to.
type receiverSession struct {
	receiver string
	session  ReceiverSession
}

// reconcileReceiver applies the session when the Player matches a
// Receiver input, and returns the Player's status.receiver.
//
// In the pass that turns the Player off, the powerAsk of off is written
// before the session with awake false. In that order equipment-operator
// puts the room in standby before it reads the session that marks the
// room asleep. A request can only be written beside a session that this
// process has already applied or adopted, so when there is none yet,
// as on the first pass after the operator starts, the request is written
// after the session. outcome records the result of the powerAsk.
func (o *operator) reconcileReceiver(player *Player, unfinished bool, step powerStep, outcome *powerOutcome) *PlayerReceiverStatus {
	receiver, input, matched := o.matchReceiver(player)
	key := playerKey(player.Metadata.Namespace, player.Metadata.Name)
	if !matched {
		// When listing the Receivers failed, the wiring is unknown, so
		// the Player keeps its entry in o.playerReceivers from the last pass.
		// Clearing the entry would drop every input request and every
		// Television screen request for this Player until the next pass.
		if !o.receiverLookup().failed {
			o.playerReceivers.set(key, receiverWiring{})
		}
		return nil
	}
	name := receiver.Metadata.Name
	asked := false
	var askErr error
	if step.turningOff() {
		askErr = o.askReceiverPower(name, receiverPowerOff)
		asked = !errors.Is(askErr, errNoSession)
	}
	o.applySession(player, receiver, input, unfinished, o.awake(player, receiver))
	if step.turningOff() && !asked {
		askErr = o.askReceiverPower(name, receiverPowerOff)
	}
	switch {
	case step.turningOff() && askErr != nil:
		outcome.fail(powerReasonReceiverAskFailed,
			fmt.Sprintf("writing powerAsk %s to Receiver %s: %v", receiverPowerOff, name, askErr))
	case step.turningOff():
		outcome.record("requested %s from Receiver %s", receiverPowerOff, name)
	}
	// The bus reader finds this Player's Receiver in o.playerReceivers. It uses
	// the entry to write an input request for a controller button press,
	// and to relay a Television's request for the Player's screen.
	o.playerReceivers.set(key, receiverWiring{
		name:          receiver.Metadata.Name,
		inputSelected: receiverCondition(receiver, receiverInputSelectedCondition) == conditionTrue,
	})
	return &PlayerReceiverStatus{
		Name:      receiver.Metadata.Name,
		Input:     input,
		Reachable: receiverReachable(receiver),
	}
}

// awake computes the session's awake flag. It is false while spec.power
// is Off. Otherwise it follows the panel desire, the on or off message
// that the Player's idle client publishes on the bus when its panel
// should be lit or dark. An off desire from before the Player's last
// turn-on does not count (panelCache.desireFor).
//
// When no desire has arrived, awake keeps the flag of the session this
// Player already has on this Receiver. The operator can start before
// the bus delivers the retained desire, and a default of true would
// then power the equipment on in a dark room. With no desire and no
// session, awake returns true.
func (o *operator) awake(player *Player, receiver *Receiver) bool {
	if specPower(player) == playerPowerOff {
		return false
	}
	switch o.panels.desireFor(playerKey(player.Metadata.Namespace, player.Metadata.Name), player.Status.PoweredOnAt) {
	case panelDesireOff:
		return false
	case "":
		current := currentSession(receiver)
		if current != nil && current.Player == player.Metadata.Namespace+"/"+player.Metadata.Name {
			return current.Awake
		}
	}
	return true
}

// matchReceiver finds the Receiver input connected to the Player's
// screen. matchedScreen keeps the screen through a short gap in the
// claim's allocation, and screengap.go explains why.
func (o *operator) matchReceiver(player *Player) (*Receiver, string, bool) {
	found, resolved := o.matchedScreen(player)
	if !resolved {
		return nil, "", false
	}
	return o.receiverLookup().matchFor(found.node, found.monitor)
}

// applyReceiverSession applies an active session before the playback
// pod is created, so the equipment is on and set to the right input by
// the time mpv draws.
//
// A Play that starts wakes the room, so awake is true unless spec.power
// is Off. Before this call, turnOnForPlay turns on a Player that is
// off. When the API server rejected that change, the Player is still
// off, and the session is applied with awake false. The next pass
// computes awake from the panel desire again.
func (o *operator) applyReceiverSession(player *Player) {
	receiver, input, matched := o.matchReceiver(player)
	if !matched {
		return
	}
	o.applySession(player, receiver, input, true, specPower(player) == playerPowerOn)
}

// askReceiverPower writes a powerAsk with the given action to a
// Receiver, in the same apply as media-operator's session there.
func (o *operator) askReceiverPower(receiver, action string) error {
	return o.sessions.ask(o.client, receiver, func(asks *receiverAsks) {
		asks.power = &ReceiverAction{Action: action, At: nextAskTime(previousAt(asks.power), time.Now())}
	})
}

// applySession writes status.session under media-operator's field
// manager when the session differs from the one this process last
// applied. equipment-operator acts once on each change of power or
// input, so it must not receive an unchanged session again. A Play that
// starts or ends, and a panel that turns on or off, each change one
// flag, and that change is applied. When the Player has moved to
// another Receiver, applySession first removes the session from the
// old one.
func (o *operator) applySession(player *Player, receiver *Receiver, input string, active, awake bool) {
	namespace, name := player.Metadata.Namespace, player.Metadata.Name
	key := playerKey(namespace, name)
	session := ReceiverSession{
		Player: namespace + "/" + name,
		Input:  input,
		Active: active,
		Awake:  awake,
	}
	previous, tracked := o.receiverSessions[key]
	current := currentSession(receiver)
	statusHasSession := receiver.Status.Session != nil
	switch {
	case tracked && previous.receiver == receiver.Metadata.Name && previous.session == session:
		// This process already applied this session.
	case !tracked && sameSession(current, session) && statusHasSession:
		// An earlier process of media-operator applied this session, so
		// record it as applied and do not send it again.
		o.receiverSessions[key] = receiverSession{receiver: receiver.Metadata.Name, session: session}
		o.sessions.adopt(receiver.Metadata.Name, session)
	case !tracked && sameSession(current, session):
		// An earlier version of media-operator wrote this session to
		// spec.session. Copy it to status.session, so that spec.session
		// can be released. equipment-operator treats a session that
		// moves unchanged from spec to status as the same session, so
		// the receiver gets no command.
		if !o.writeSession(player, receiver, session, previous, tracked) {
			return
		}
		statusHasSession = true
	default:
		if !o.writeSession(player, receiver, session, previous, tracked) {
			return
		}
		statusHasSession = true
	}
	o.releaseSpecSession(key, receiver, statusHasSession)
}

// sameSession reports whether the session the Receiver already has is
// the one media-operator would apply. A session that still has a topic
// is not the same, so the apply that follows removes the topic.
func sameSession(current *ReceiverSession, session ReceiverSession) bool {
	if current == nil || current.VolumeTopic != "" || current.PowerTopic != "" {
		return false
	}
	return current.base() == session
}

// writeSession removes the session a moved Player left on its old
// Receiver, then applies the new session. It returns true when the new
// session was written.
func (o *operator) writeSession(player *Player, receiver *Receiver, session ReceiverSession, previous receiverSession, tracked bool) bool {
	key := playerKey(player.Metadata.Namespace, player.Metadata.Name)
	if tracked && previous.receiver != receiver.Metadata.Name {
		if !o.removeSession(previous.receiver) {
			return false
		}
		delete(o.receiverSessions, key)
		logLine(o.log, "player %s: removed the session from Receiver %s, because the Player moved to Receiver %s",
			key, previous.receiver, receiver.Metadata.Name)
	}
	if err := o.sessions.apply(o.client, receiver.Metadata.Name, session); err != nil {
		fmt.Fprintf(os.Stderr, "writing the session on Receiver %s: %v\n",
			receiver.Metadata.Name, err)
		if !o.receiverFaults[key] {
			if o.receiverFaults == nil {
				o.receiverFaults = map[string]bool{}
			}
			o.receiverFaults[key] = true
			o.recorder.Warning(playerRef(player), reasonReceiverWriteFailed,
				fmt.Sprintf("Writing the session to Receiver %s failed, and the operator writes it again on each pass: %v", receiver.Metadata.Name, err))
		}
		return false
	}
	if o.receiverFaults[key] {
		delete(o.receiverFaults, key)
		o.recorder.Normal(playerRef(player), reasonReceiverWriteRecovered,
			fmt.Sprintf("Wrote the session to Receiver %s: input %s", receiver.Metadata.Name, session.Input))
	}
	o.receiverSessions[key] = receiverSession{receiver: receiver.Metadata.Name, session: session}
	logLine(o.log, "player %s: applied the session on receiver %s: input %s, active %t, awake %t",
		key, receiver.Metadata.Name, session.Input, session.Active, session.Awake)
	return true
}

// releaseSpecSession removes the spec.session that an earlier version of
// media-operator wrote, once status.session has the session.
// equipment-operator uses spec.session when status.session is empty, so
// releasing first would briefly end the session.
//
// The release is sent at most once per process for each Receiver. The
// API server removes only fields that this field manager owns, so a
// spec.session written by another manager stays, and a second apply
// would change nothing.
func (o *operator) releaseSpecSession(key string, receiver *Receiver, statusHasSession bool) {
	name := receiver.Metadata.Name
	if receiver.Spec.Session == nil {
		o.specReleased[name] = true
		return
	}
	if !statusHasSession || o.specReleased[name] {
		return
	}
	if err := ReleaseReceiverSpecSession(o.client, name); err != nil {
		fmt.Fprintf(os.Stderr, "releasing spec.session on Receiver %s: %v\n", name, err)
		return
	}
	o.specReleased[name] = true
	logLine(o.log, "player %s: released spec.session on receiver %s, because status.session holds the session",
		key, name)
}

// removeSession removes media-operator's session from one Receiver,
// first the spec.session that an earlier version wrote, then
// status.session. It returns true when the session is gone. A Receiver
// that no longer exists has no session, so not found counts as success.
func (o *operator) removeSession(name string) bool {
	if !o.specReleased[name] {
		err := ReleaseReceiverSpecSession(o.client, name)
		if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "releasing spec.session on Receiver %s: %v\n", name, err)
			return false
		}
		o.specReleased[name] = true
	}
	err := o.sessions.lift(o.client, name)
	if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "lifting the session on Receiver %s: %v\n", name, err)
		return false
	}
	return true
}

// retainSessions removes the session of every Player that is not in
// matched. A Play that ends does not remove the session. It only sets
// the active flag to false. When removing a session fails, the entry
// stays in receiverSessions, so the next pass tries again.
func (o *operator) retainSessions(matched map[string]bool) {
	for key, previous := range o.receiverSessions {
		if matched[key] {
			continue
		}
		if !o.removeSession(previous.receiver) {
			continue
		}
		delete(o.receiverSessions, key)
		logLine(o.log, "player %s: removed the session from Receiver %s, because the Player matches no Receiver input now",
			key, previous.receiver)
	}
}

// playerHasUnfinishedPlay reports whether a Play in the Player's
// namespace names this Player and has any phase except Finished, which
// is the same test that keeps the playback pod. A Play created in this
// pass has no phase yet, and a Failed Play keeps its pod until it is
// recreated, so a test for Pending or Running would report the room
// idle while the pod is still up.
func playerHasUnfinishedPlay(player *Player, plays []Play) bool {
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
