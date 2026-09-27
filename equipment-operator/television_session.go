package main

// The session match: the Deployment's half of the TV wake. A Receiver's
// session wakes the room on its input, and the input names the Display
// whose picture it carries. The Television whose status.displays lists
// that Display is the TV the picture reaches, so the Deployment writes
// that Television's status.session, and the node workload that speaks
// for the Display wakes the TV. The path from a Player to its TV is a
// lookup: the Player's session, the input's Display, and the
// Television.
//
// The Deployment writes a new wokeAt only for a change it sees happen
// while it runs: a flag of a standing session that turns on, the
// remote's power button that turns the receiver on, or a session that
// appears with a flag on, such as a Play that starts on a Player with
// no standing session. What it finds in its first pass is not a change
// it saw, so a session that stands at an operator restart is adopted
// and wakes nothing. A session that returns for the same Player and
// Display while its lift waits is the same session, such as one the
// media operator lifted for a moment, and wakes nothing either.

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// A Receiver session that ends leaves its TV's status.session in place
// for sessionLiftDelay, so a session that the media operator lifts and
// writes again, such as while a Player's idle pod restarts, finds it
// still there. A removal the API server refuses is tried again after
// sessionLiftRetry.
var (
	sessionLiftDelay = 60 * time.Second
	sessionLiftRetry = 10 * time.Second
)

// televisionSessions is the Deployment's one writer of every
// Television's status.session. Each write reads the Television and then
// applies what it read with one change, so two writes at once could
// each undo the other's change. The mutex serializes them: the reconcile
// pass, which reports the flags, and a session's own goroutine, which
// reports the remote's power button, write one after the other.
type televisionSessions struct {
	client *Client
	mutex  sync.Mutex
	// lifts holds the removal waiting for each Player whose session
	// ended, and the Display that session showed.
	lifts map[string]pendingLift
	// shown is the Display each Player's session last showed, which a
	// lift remembers.
	shown map[string]string
	// live says the operator is past its first pass, so a session that
	// appears is a change it saw happen.
	live bool
}

// pendingLift is one removal that waits.
type pendingLift struct {
	timer   *time.Timer
	display string
}

func newTelevisionSessions(client *Client) *televisionSessions {
	return &televisionSessions{client: client, lifts: map[string]pendingLift{}, shown: map[string]string{}}
}

// markLive records the end of the operator's first pass: from then on,
// a session that appears awake wakes its TV.
func (t *televisionSessions) markLive() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.live = true
}

// room answers the link one Receiver session uses to tell its TV what
// it did.
func (t *televisionSessions) room(log *receiverLog, player, input string, monitor func(input string) string) *roomTelevision {
	return &roomTelevision{sessions: t, log: log, player: player, input: input, monitor: monitor}
}

// roomTelevision is one Receiver session's link to the TV of its room.
// It resolves the TV at each event, because the Displays a Television
// lists follow its bus, and a bus can change while a session stands.
type roomTelevision struct {
	sessions *televisionSessions
	log      *receiverLog
	player   string
	input    string
	// monitor answers the Display a declared input names.
	monitor func(input string) string
}

// opened reports a session that starts. A session that appears awake
// while the operator runs wakes its TV, the way woke does, with trigger
// for the line. Any other session is adopted: the TV's status.session
// names this Player and Display with the session's awake state, and
// keeps the wokeAt the TV already holds for them, so nothing wakes.
// That is a session that stands at the operator's first pass, one that
// starts idle, and one that returns for the same Player and Display
// while its lift waits. A start also ends a removal that waits for this
// Player, and removes this Player's session from any other TV at once,
// because the session now shows another Display.
func (r *roomTelevision) opened(awake bool, trigger string) {
	t := r.sessions
	t.mutex.Lock()
	defer t.mutex.Unlock()
	pending, lifted := t.lifts[r.player]
	if lifted {
		pending.timer.Stop()
		delete(t.lifts, r.player)
	}
	list, display, television, ok := r.resolve()
	if !ok {
		return
	}
	t.shown[r.player] = display
	returning := lifted && pending.display == display
	for _, other := range namedBy(list, r.player) {
		if television == nil || other.Metadata.Name != television.Metadata.Name {
			t.write(other.Metadata.Name, nil)
		}
	}
	if television == nil {
		return
	}
	if t.live && awake && !returning {
		r.wake(trigger, display, television)
		return
	}
	adopted := &TelevisionSession{Player: r.player, Display: display, Awake: awake}
	if held := television.Status.Session; held != nil && held.Player == r.player && held.Display == display {
		adopted.WokeAt = held.WokeAt
	}
	if held := television.Status.Session; held != nil && *held == *adopted {
		return
	}
	t.write(television.Metadata.Name, adopted)
}

// woke writes a new wokeAt on the TV that shows the session's input,
// with the session awake. The session calls it only for a wake it saw
// happen.
func (r *roomTelevision) woke(trigger string) {
	t := r.sessions
	t.mutex.Lock()
	defer t.mutex.Unlock()
	_, display, television, ok := r.resolve()
	if !ok || television == nil {
		return
	}
	t.shown[r.player] = display
	r.wake(trigger, display, television)
}

// wake writes a new wokeAt on a TV, and its one line. The caller holds
// the mutex.
func (r *roomTelevision) wake(trigger, display string, television *Television) {
	session := &TelevisionSession{Player: r.player, Display: display, Awake: true, WokeAt: time.Now().UTC().Format(wakeTimeLayout)}
	line := fmt.Sprintf("%s; asked Television %s to wake and show Display %s", trigger, television.Metadata.Name, display)
	if err := ApplyTelevisionSession(r.sessions.client, television.Metadata.Name, session); err != nil {
		r.log.refused(line, err)
		return
	}
	r.log.printf("%s", line)
}

// slept marks the TV's session asleep, which stops a wake in progress,
// so the adapter claims no input in a room a person just turned off.
// It sends the TV nothing: the receiver's own CEC link puts the TV in
// standby with it.
func (r *roomTelevision) slept() {
	t := r.sessions
	t.mutex.Lock()
	defer t.mutex.Unlock()
	list, err := ListTelevisions(t.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing Televisions for Player %s's session: %v\n", r.player, err)
		return
	}
	for _, television := range namedBy(list.Items, r.player) {
		asleep := *television.Status.Session
		if !asleep.Awake {
			continue
		}
		asleep.Awake = false
		t.write(television.Metadata.Name, &asleep)
	}
}

// resolve reads the Televisions and answers them, the session's
// Display, and the Television that shows it, or nil for none. ok is
// false when the input names no Display or the list failed.
func (r *roomTelevision) resolve() ([]Television, string, *Television, bool) {
	display := r.monitor(r.input)
	if display == "" {
		return nil, "", nil, false
	}
	list, err := ListTelevisions(r.sessions.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing Televisions for Player %s's session: %v\n", r.player, err)
		return nil, "", nil, false
	}
	return list.Items, display, televisionShowing(list.Items, display), true
}

// lift removes a Player's session from every TV after sessionLiftDelay,
// unless a session of the same Player starts first. The lift remembers
// the Display the session showed, so a session that returns on it is
// known for the same one. A removal the API
// server refuses is tried again after sessionLiftRetry. A lift that
// waits is not a lift at operator shutdown: stop drops it, and the next
// operator adopts the session.
func (t *televisionSessions) lift(player string) {
	if t == nil {
		return
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.schedule(player, sessionLiftDelay)
}

// schedule arms the removal of a Player's session. The caller holds the
// mutex.
func (t *televisionSessions) schedule(player string, after time.Duration) {
	if pending, held := t.lifts[player]; held {
		pending.timer.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(after, func() {
		t.mutex.Lock()
		defer t.mutex.Unlock()
		if t.lifts[player].timer != timer {
			return
		}
		delete(t.lifts, player)
		if !t.remove(player) {
			t.schedule(player, sessionLiftRetry)
		}
	})
	t.lifts[player] = pendingLift{timer: timer, display: t.shown[player]}
}

// remove takes a Player's session off every TV, and answers whether
// every write went through. The caller holds the mutex.
func (t *televisionSessions) remove(player string) bool {
	list, err := ListTelevisions(t.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing Televisions to lift Player %s's session: %v\n", player, err)
		return false
	}
	removed := true
	for _, television := range namedBy(list.Items, player) {
		removed = t.write(television.Metadata.Name, nil) && removed
	}
	return removed
}

// stop drops every removal that waits, at operator shutdown.
func (t *televisionSessions) stop() {
	if t == nil {
		return
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()
	for player, pending := range t.lifts {
		pending.timer.Stop()
		delete(t.lifts, player)
	}
}

// write applies one session, and answers whether the API server took
// it. The caller holds the mutex.
func (t *televisionSessions) write(name string, session *TelevisionSession) bool {
	if err := ApplyTelevisionSession(t.client, name, session); err != nil {
		fmt.Fprintf(os.Stderr, "writing the session of Television %s: %v\n", name, err)
		return false
	}
	return true
}

// namedBy answers the Televisions whose session names a Player.
func namedBy(list []Television, player string) []Television {
	return slices.DeleteFunc(slices.Clone(list), func(television Television) bool {
		return television.Status.Session == nil || television.Status.Session.Player != player
	})
}

// televisionShowing answers the Television that speaks for the TV
// whose status.displays lists a Display, and nil when no Television
// lists it. A bus has one TV, so of two Televisions on one bus, the one
// in charge gets the session. A Display on two buses cannot happen,
// because only the adapter that names the Display announces its
// address; the first bus by name wins if a spec says otherwise.
func televisionShowing(list []Television, display string) *Television {
	var buses []string
	for _, television := range list {
		listed := slices.ContainsFunc(television.Status.Displays, func(shown TelevisionDisplay) bool { return shown.Name == display })
		if listed && !slices.Contains(buses, television.bus()) {
			buses = append(buses, television.bus())
		}
	}
	slices.SortFunc(buses, strings.Compare)
	for _, bus := range buses {
		if chosen := televisionFor(list, bus); chosen != nil {
			return chosen
		}
	}
	return nil
}
