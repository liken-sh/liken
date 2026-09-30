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
// remote's power button that turns the room on, or a session that
// appears with a flag on, such as a Play that starts on a Player with
// no standing session. What it finds in its first pass is not a change
// it saw, so a session that stands at an operator restart is adopted
// and wakes nothing. A session that returns for the same Player and
// Display while its lift waits is the same session, such as one the
// media operator lifted for a moment, and wakes nothing either.
//
// The remote's power button is the one event that turns the TV off. A
// press that finds the room on writes a standbyAt, and the node
// workload sends the TV Standby. A session that sleeps or ends writes
// none, because a TV in a living room shows other inputs, such as a
// streaming player, while the room's player is idle.

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/informer"
)

// A Receiver session that ends leaves its TV's status.session in place
// for sessionLiftDelay, so a session that the media operator lifts and
// writes again, such as while a Player's idle pod restarts, finds it
// still there. A removal the API server refuses is tried again after
// sessionLiftRetry.
const (
	sessionLiftDelay = 60 * time.Second
	sessionLiftRetry = 10 * time.Second
)

// televisionSessions is the Deployment's one writer of every
// Television's status.session. Each write reads the Television and then
// applies what it read with one change, so two writes at once could
// each undo the other's change. The mutex serializes them: the reconcile
// pass, which reports the flags, and a session's own goroutine, which
// reports the remote's power button, write one after the other.
//
// Every list and write waits out a 429 and asks again, because the API
// server answers 429 for a moment after a CRD changes. The shared
// client waits for at most ten seconds, and stop ends the wait sooner.
// An event whose writes still fail is held in retries and tried again
// on each later pass, so a refusal delays a TV's session and never
// drops it. After stop, a list or a write sends nothing.
type televisionSessions struct {
	client *Client
	// televisions holds the Television watch's store, which list reads.
	televisions *watchStore
	// ctx is the sessions' life, and stop ends it. After it ends, a list
	// or a write sends nothing, and a wait for a 429 ends at once. A
	// request already sent runs to its answer, because the Deployment
	// releases its Lease after its last write (work.go).
	ctx    context.Context
	cancel context.CancelFunc
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
	// retries holds, for each Player, the last event whose writes did not
	// all go through. It holds the decision the event made, so a retry on
	// a live pass still adopts what the first pass adopted. A later event
	// of the same Player replaces it, because it is newer.
	retries map[string]func() bool
	// screens maps each Player to the session that holds its room, which
	// relays an ask for the Player's screen (television_screen.go).
	screens map[string]*session
	// liftDelay and liftRetry are sessionLiftDelay and sessionLiftRetry
	// as they stood when the writer was made. A lift's timer reads its
	// own writer's copy, so a timer that outlives the writer never reads
	// the package values while something else sets them.
	liftDelay, liftRetry time.Duration
}

// pendingLift is one removal that waits.
type pendingLift struct {
	timer   *time.Timer
	display string
}

func newTelevisionSessions(client *Client) *televisionSessions {
	ctx, cancel := context.WithCancel(context.Background())
	return &televisionSessions{
		client: client, ctx: ctx, cancel: cancel,
		lifts: map[string]pendingLift{}, shown: map[string]string{}, retries: map[string]func() bool{},
		liftDelay: sessionLiftDelay, liftRetry: sessionLiftRetry,
	}
}

// attempt runs one event's writes, and holds the event for a later pass
// when they did not all go through. The caller holds the mutex.
func (t *televisionSessions) attempt(player string, settle func() bool) {
	if settle() {
		delete(t.retries, player)
		return
	}
	t.retries[player] = settle
}

// retry runs each held event again. The reconcile pass calls it, so an
// event the API server refused is tried on every pass until it goes
// through.
func (t *televisionSessions) retry() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	for player, settle := range t.retries {
		if settle() {
			delete(t.retries, player)
		}
	}
}

// list reads the Televisions, and waits out a 429 for up to the shared
// client's ten seconds, or until stop. After stop it sends nothing and
// answers the stop. A session event reads the status.session it wrote
// on the event before, and the store can hold the copy from before that
// write until the write's own event arrives, so the read replaces such
// a copy with the API server's (objectcache.go).
func (t *televisionSessions) list() (*TelevisionList, error) {
	if err := t.ctx.Err(); err != nil {
		return nil, err
	}
	return readTelevisions(t.client.withWaits(t.ctx), t.televisions)
}

// apply writes one Television's status.session, and waits out a 429
// for up to the shared client's ten seconds, or until stop. After stop
// it sends nothing and answers the stop.
func (t *televisionSessions) apply(name string, session *TelevisionSession) error {
	if err := t.ctx.Err(); err != nil {
		return err
	}
	return ApplyTelevisionSession(t.client.withWaits(t.ctx), name, session)
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
	wakes := t.live && awake
	t.attempt(r.player, func() bool { return r.open(awake, wakes, lifted, pending.display, trigger) })
}

// open writes what opened decided, and answers whether every write went
// through. wakes is decided when the session starts, so a retry on a
// later pass does the same thing. The caller holds the mutex.
func (r *roomTelevision) open(awake, wakes, lifted bool, liftedDisplay, trigger string) bool {
	t := r.sessions
	list, display, television, ok := r.resolve()
	if !ok {
		return false
	}
	if display == "" {
		return true
	}
	t.shown[r.player] = display
	returning := lifted && liftedDisplay == display
	settled := true
	for _, other := range namedBy(list, r.player) {
		if television == nil || other.Metadata.Name != television.Metadata.Name {
			settled = t.write(other.Metadata.Name, nil) && settled
		}
	}
	if television == nil {
		return settled
	}
	if wakes && !returning {
		return r.wake(trigger, display, television) && settled
	}
	adopted := &TelevisionSession{Player: r.player, Display: display, Awake: awake}
	if held := television.Status.Session; held != nil && held.Player == r.player && held.Display == display {
		adopted.WokeAt, adopted.StandbyAt = held.WokeAt, held.StandbyAt
	}
	if held := television.Status.Session; held != nil && *held == *adopted {
		return settled
	}
	return t.write(television.Metadata.Name, adopted) && settled
}

// woke writes a new wokeAt on the TV that shows the session's input,
// with the session awake. The session calls it only for a wake it saw
// happen.
func (r *roomTelevision) woke(trigger string) {
	t := r.sessions
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.attempt(r.player, func() bool {
		_, display, television, ok := r.resolve()
		if !ok {
			return false
		}
		if television == nil {
			return true
		}
		t.shown[r.player] = display
		return r.wake(trigger, display, television)
	})
}

// wake writes a new wokeAt on a TV, and its one line, and answers
// whether the write went through. The caller holds the mutex.
func (r *roomTelevision) wake(trigger, display string, television *Television) bool {
	session := &TelevisionSession{Player: r.player, Display: display, Awake: true, WokeAt: time.Now().UTC().Format(wakeTimeLayout)}
	line := fmt.Sprintf("%s; asked Television %s to wake and show Display %s", trigger, television.Metadata.Name, display)
	if err := r.sessions.apply(television.Metadata.Name, session); err != nil {
		r.log.refused(line, err)
		return false
	}
	r.log.printf("%s", line)
	return true
}

// cecPowerReadWait bounds the wait of a power press for the node
// workload's read of the TV's power. The read is one question that the
// TV answers within a second, and two writes to the API server.
const cecPowerReadWait = 3 * time.Second

// television answers the Television that shows the session's input
// and its power, for a power press that decides whether the room is on.
// Both are empty when no Television lists the input's Display, and the
// power is empty when the TV does not answer.
//
// No timer asks the TV for its power, and a TV that a person turned on
// or off with its own remote can send nothing an adapter hears, so
// status.power can be older than the press. When an adapter in Control
// finds the TV, the press asks the node workload for a read through
// the session's powerReadAt and waits up to cecPowerReadWait for
// status.powerRead to answer it. That includes a TV that is not
// Reachable because it gave no power, at the join scan or on two reads
// in a row: status.power is then empty and stays empty until something
// asks again, and a TV that is on would otherwise let a receiver in
// standby decide that the room is off. A press that gets no answer in
// time decides from status.power, and the log says so. A TV that no
// adapter in Control finds gets no read, and status.power is then
// absent or from what a bus in Listen heard.
func (r *roomTelevision) television() (string, string) {
	t := r.sessions
	display := r.monitor(r.input)
	if display == "" {
		return "", ""
	}
	t.mutex.Lock()
	list, err := t.list()
	if err != nil {
		t.mutex.Unlock()
		fmt.Fprintf(os.Stderr, "listing Televisions for Player %s's power press: %v\n", r.player, err)
		return "", ""
	}
	television := televisionShowing(list.Items, display)
	if television == nil {
		t.mutex.Unlock()
		return "", ""
	}
	name, held := television.Metadata.Name, television.Status.Session
	if !readable(television) || held == nil || held.Player != r.player || held.Display != display {
		t.mutex.Unlock()
		return name, television.Status.Power
	}
	asked := *held
	asked.PowerReadAt = time.Now().UTC().Format(wakeTimeLayout)
	err = t.apply(name, &asked)
	t.mutex.Unlock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "asking the node workload to read Television %s's power: %v\n", name, err)
		return name, television.Status.Power
	}
	power, answered := t.awaitPowerRead(name, asked.PowerReadAt)
	if !answered {
		fmt.Fprintf(os.Stderr, "the node workload did not read Television %s's power within %s, so the power press decides from status.power %q\n",
			name, cecPowerReadWait, television.Status.Power)
		return name, television.Status.Power
	}
	return name, power
}

// readable answers whether an adapter in Control finds the TV, so a
// read of its power reaches it: the TV is Reachable, or it acknowledges
// its address and gave no power.
func readable(television *Television) bool {
	reachable := conditionOf(television.Status.Conditions, conditionReachable)
	return reachable.Status == ConditionTrue || reachable.Reason == reasonNoPower
}

// awaitPowerRead waits until a Television's status.powerRead answers
// the request at, and answers the power it states. It reads the
// Television once, and again at each change the Deployment's
// Television watch takes into its store, so it waits on the API
// server's events and not on a timer, and a press opens no watch of
// its own. It takes the store's change channel before each read, so an
// answer that lands during the read is not lost. It answers false when
// cecPowerReadWait ends first. With no Television watch running, such
// as in the moment before the CECBus loop opens it, only the first
// read and a watch that opens during the wait can find the answer.
func (t *televisionSessions) awaitPowerRead(name, at string) (string, bool) {
	ctx, cancel := context.WithTimeout(t.ctx, cecPowerReadWait)
	defer cancel()
	for {
		changed := t.televisions.changed()
		held := informer.Held{View: t.televisions.view(), Versions: t.client.versions.televisions}
		if television, err := informer.ReadOne[Television](t.client.Client, held, name, televisionPath(name)); err == nil {
			if read := television.Status.PowerRead; read != nil && read.At == at {
				return read.Power, true
			}
		}
		select {
		case <-ctx.Done():
			return "", false
		case <-changed:
		}
	}
}

// standby writes a new standbyAt on the TV that shows the session's
// input, with the session asleep, which stops a wake in progress. The
// node workload that speaks for the Display then sends the TV Standby.
// Only the remote's power button calls it: a TV in a living room shows
// other inputs while the room's player is idle, so a sleep of the
// session never turns the TV off.
func (r *roomTelevision) standby(trigger string) {
	t := r.sessions
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.attempt(r.player, func() bool {
		_, display, television, ok := r.resolve()
		if !ok {
			return false
		}
		if television == nil {
			return true
		}
		t.shown[r.player] = display
		session := &TelevisionSession{Player: r.player, Display: display, StandbyAt: time.Now().UTC().Format(wakeTimeLayout)}
		if held := television.Status.Session; held != nil && held.Player == r.player && held.Display == display {
			session.WokeAt = held.WokeAt
		}
		line := fmt.Sprintf("%s; asked Television %s to go to standby", trigger, television.Metadata.Name)
		if err := t.apply(television.Metadata.Name, session); err != nil {
			r.log.refused(line, err)
			return false
		}
		r.log.printf("%s", line)
		return true
	})
}

// slept marks the TV's session asleep, which stops a wake in progress,
// so the adapter claims no input in a room that went dark. It sends the
// TV nothing: a Play that ends or a screen that sleeps leaves the TV
// free to show another input.
func (r *roomTelevision) slept() {
	t := r.sessions
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.attempt(r.player, func() bool {
		list, err := t.list()
		if err != nil {
			fmt.Fprintf(os.Stderr, "listing Televisions for Player %s's session: %v\n", r.player, err)
			return false
		}
		settled := true
		for _, television := range namedBy(list.Items, r.player) {
			asleep := *television.Status.Session
			if !asleep.Awake {
				continue
			}
			asleep.Awake = false
			settled = t.write(television.Metadata.Name, &asleep) && settled
		}
		return settled
	})
}

// resolve reads the Televisions and answers them, the session's
// Display, and the Television that shows it, or nil for none. The
// Display is empty when the input names none, and then nothing is read.
// ok is false only when the list failed.
func (r *roomTelevision) resolve() ([]Television, string, *Television, bool) {
	display := r.monitor(r.input)
	if display == "" {
		return nil, "", nil, true
	}
	list, err := r.sessions.list()
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
	// The session ended, so an event of it that waits for a retry is
	// moot; the lift has retries of its own.
	delete(t.retries, player)
	t.schedule(player, t.liftDelay)
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
			t.schedule(player, t.liftRetry)
		}
	})
	t.lifts[player] = pendingLift{timer: timer, display: t.shown[player]}
}

// remove takes a Player's session off every TV, and answers whether
// every write went through. The caller holds the mutex.
func (t *televisionSessions) remove(player string) bool {
	list, err := t.list()
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

// stop drops every removal that waits, at operator shutdown. It ends
// the sessions' context before it takes the mutex, because a write that
// waits holds the mutex. A write that waits out a 429 then stops
// waiting, and every later list and write sends nothing, so stop waits
// for at most the one request already sent, which the shared client's
// request timeout bounds.
func (t *televisionSessions) stop() {
	if t == nil {
		return
	}
	t.cancel()
	t.mutex.Lock()
	defer t.mutex.Unlock()
	for player, pending := range t.lifts {
		pending.timer.Stop()
		delete(t.lifts, player)
	}
	clear(t.retries)
}

// write applies one session, and answers whether the API server took
// it. The caller holds the mutex.
func (t *televisionSessions) write(name string, session *TelevisionSession) bool {
	if err := t.apply(name, session); err != nil {
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
