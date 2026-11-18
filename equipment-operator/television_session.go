package main

// This file writes each Television's status.session from the Receiver
// sessions. A session's input names a Display, and the session goes to
// the Television whose status.displays lists that Display. The CEC node
// workload whose adapter announces that Display's physical address
// reads status.session, and wakes the TV or sends it Standby.
//
// A new wokeAt makes the node workload wake the TV, so the Deployment
// writes one only for a change it sees while it runs: the active or
// awake flag of a session turns on, a powerAsk turns the room on, or a
// session starts with a flag on. A session that already
// exists at the operator's first pass is adopted, so a restart of the
// operator wakes nothing.
//
// When a session ends, the Deployment waits sessionLiftDelay before it
// removes the session from the Television. This delayed removal is a
// lift. A session that starts again for the same Player and Display
// before its lift runs, such as while a Player's idle pod restarts, is
// adopted and wakes nothing.
//
// Only a powerAsk that turns the room off turns the TV off, with a
// standbyAt. A
// session that sleeps or ends writes none, because the TV can be
// showing another input, such as a streaming player, while the room's
// player is idle.

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

// sessionLiftRetry is the wait before a lift that the API server
// refused runs again.
const (
	sessionLiftDelay = 60 * time.Second
	sessionLiftRetry = 10 * time.Second
)

// televisionSessions writes every Television's status.session. Each
// write reads the Television and writes back its session with one
// change, so two writes at the same time could undo each other. The
// mutex serializes the reconcile pass, which reports the flags, and
// each session's goroutine, which applies each powerAsk.
//
// The shared client retries a 429 for up to ten seconds, because the
// API server returns 429 for a moment after a CRD changes. When an
// event's writes still fail, the event stays in retries and runs again
// on each later pass, so a refused write delays a TV's session but does
// not drop it.
type televisionSessions struct {
	client *Client
	// televisions is the store of the Deployment's Television watch.
	televisions *watchStore
	// stop cancels ctx. After that, list and apply send nothing, and a
	// wait after a 429 ends at once. A request already sent is not
	// canceled, because a canceled write can still reach the API server
	// after another replica has taken the Lease (see withWaits).
	ctx    context.Context
	cancel context.CancelFunc
	mutex  sync.Mutex
	// lifts holds the waiting lift of each Player whose session ended.
	lifts map[string]pendingLift
	// lastDisplay is the Display that each Player's session last showed.
	lastDisplay map[string]string
	// live turns true when the operator's first pass ends. A session that
	// opens before then already existed, so it is adopted.
	live bool
	// retries holds, for each Player, the latest event whose writes did
	// not all succeed. The function keeps the decision that the event
	// made, such as whether to wake, so a retry does not decide again.
	retries map[string]func() bool

	liftDelay, liftRetry time.Duration
}

// pendingLift is a lift that has not run yet, with the Display that the
// session showed when it ended. A session that starts again on that
// Display is adopted.
type pendingLift struct {
	timer   *time.Timer
	display string
}

func newTelevisionSessions(client *Client) *televisionSessions {
	ctx, cancel := context.WithCancel(context.Background())
	return &televisionSessions{
		client: client, ctx: ctx, cancel: cancel,
		lifts: map[string]pendingLift{}, lastDisplay: map[string]string{}, retries: map[string]func() bool{},
		liftDelay: sessionLiftDelay, liftRetry: sessionLiftRetry,
	}
}

// attempt runs one event's writes, and saves them in retries when any
// of them fails. The caller holds the mutex.
func (t *televisionSessions) attempt(player string, writes func() bool) {
	if writes() {
		delete(t.retries, player)
		return
	}
	t.retries[player] = writes
}

// retry runs the saved writes again. The reconcile pass calls it at the
// end of every pass.
func (t *televisionSessions) retry() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	for player, writes := range t.retries {
		if writes() {
			delete(t.retries, player)
		}
	}
}

// list reads the Televisions from the watch store. An event must see
// the status.session that the previous event wrote, and the store can
// still hold the copy from before that write until its watch event
// arrives. So readTelevisions replaces such a copy with the API
// server's (objectcache.go). After stop, list sends nothing and returns
// the context's error.
func (t *televisionSessions) list() (*TelevisionList, error) {
	if err := t.ctx.Err(); err != nil {
		return nil, err
	}
	return readTelevisions(t.client.withWaits(t.ctx), t.televisions)
}

// apply writes one Television's status.session. After stop, it sends
// nothing and returns the context's error.
func (t *televisionSessions) apply(name string, session *TelevisionSession) error {
	if err := t.ctx.Err(); err != nil {
		return err
	}
	return ApplyTelevisionSession(t.client.withWaits(t.ctx), name, session)
}

// markLive records the end of the operator's first pass. After it, a
// session that starts awake wakes its TV.
func (t *televisionSessions) markLive() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.live = true
}

func (t *televisionSessions) room(log *receiverLog, player, input string, monitor func(input string) string) *roomTelevision {
	return &roomTelevision{sessions: t, log: log, player: player, input: input, monitor: monitor}
}

// roomTelevision is the roomEvents of one Receiver session. It looks up
// the Television again at each event, because a Television's
// status.displays comes from what its CEC bus reports, and that can
// change while the session runs.
type roomTelevision struct {
	sessions *televisionSessions
	log      *receiverLog
	player   string
	input    string
	// monitor returns the Display that a declared input names.
	monitor func(input string) string
}

// opened reports that the session started, and cancels the Player's
// waiting lift. A session that starts awake after the first pass wakes
// its TV, as woke does, with trigger as the reason in the log line.
// Every other session is adopted: the Television gets this Player,
// Display, and awake state, and keeps the wokeAt and standbyAt it
// already has for the same Player and Display, so nothing wakes. That
// covers a session found at the first pass, one that starts idle, and
// one that returns to the same Display before its lift runs. The
// Player's session is removed at once from every other Television.
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

// open runs the writes of opened and returns whether all of them
// succeeded. The caller holds the mutex.
func (r *roomTelevision) open(awake, wakes, lifted bool, liftedDisplay, trigger string) bool {
	t := r.sessions
	list, display, television, ok := r.resolve()
	if !ok {
		return false
	}
	if display == "" {
		return true
	}
	t.lastDisplay[r.player] = display
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

// woke writes a new wokeAt, with the session awake, on the Television
// that lists the session's Display.
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
		t.lastDisplay[r.player] = display
		return r.wake(trigger, display, television)
	})
}

// wake writes a new wokeAt on a Television, logs the result, and
// returns whether the write succeeded. The caller holds the mutex.
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

// cecPowerReadWait is how long a powerAsk waits for the node
// workload to read the TV's power. The read is one CEC query and two
// API server writes, and the TV answers the query within a second.
const cecPowerReadWait = 3 * time.Second

// television returns the name and power of the Television that lists
// the session's Display, for a powerAsk that must know whether the
// room is on. Both are empty when no Television lists the Display.
//
// status.power can be out of date, because nothing polls the
// TV, and a TV switched with its own remote sends nothing that an
// adapter hears. So when readable is true and the Television holds this
// Player's session, television writes powerReadAt in the session and
// waits up to cecPowerReadWait for the node workload to answer in
// status.powerRead. When the write fails or no answer arrives in time,
// it returns status.power.
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
		fmt.Fprintf(os.Stderr, "listing Televisions for the powerAsk of Player %s: %v\n", r.player, err)
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
		fmt.Fprintf(os.Stderr, "writing powerReadAt to the session of Television %s: %v\n", name, err)
		return name, television.Status.Power
	}
	power, answered := t.awaitPowerRead(name, asked.PowerReadAt)
	if !answered {
		fmt.Fprintf(os.Stderr, "the node workload did not read the power of Television %s within %s, so the powerAsk uses status.power %q\n",
			name, cecPowerReadWait, television.Status.Power)
		return name, television.Status.Power
	}
	return name, power
}

// readable returns whether a power read can get an answer from the TV:
// its bus is in Control mode, and the TV acknowledges logical address 0.
// That includes a TV whose Reachable condition is False with
// NoPowerStatus, because its status.power stays empty until something
// asks again. Without a read, a TV that is on would let a receiver in
// standby decide that the room is off.
func readable(television *Television) bool {
	reachable := conditionOf(television.Status.Conditions, conditionReachable)
	return reachable.Status == ConditionTrue || reachable.Reason == reasonNoPower
}

// awaitPowerRead waits until the Television's status.powerRead.at
// equals at, and returns the power it reports. It returns false when
// cecPowerReadWait ends first. It reads the Television once and again
// at each change that the Deployment's Television watch stores, so the
// press opens no watch of its own. It takes the store's change channel
// before each read, so an answer that arrives during the read is not
// lost. Before the CECBus loop starts the Television watch, only the
// first read, or a watch that starts during the wait, can find the
// answer.
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

// standby writes a new standbyAt, with the session asleep, on the
// Television that lists the session's Display. The node workload then
// stops any wake in progress and sends the TV Standby.
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
		t.lastDisplay[r.player] = display
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

// slept marks the Player's session asleep on every Television. That
// stops a wake in progress, so the adapter does not switch the TV to
// the Player's input after the room went dark. It does not turn the TV
// off.
func (r *roomTelevision) slept() {
	t := r.sessions
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.attempt(r.player, func() bool {
		list, err := t.list()
		if err != nil {
			fmt.Fprintf(os.Stderr, "listing Televisions for the session of Player %s: %v\n", r.player, err)
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

// resolve returns the Televisions, the session's Display, and the
// Television that lists it, or nil when none does. When the input names
// no Display, it lists nothing and returns an empty Display. ok is
// false only when the list failed.
func (r *roomTelevision) resolve() ([]Television, string, *Television, bool) {
	display := r.monitor(r.input)
	if display == "" {
		return nil, "", nil, true
	}
	list, err := r.sessions.list()
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing Televisions for the session of Player %s: %v\n", r.player, err)
		return nil, "", nil, false
	}
	return list.Items, display, televisionShowing(list.Items, display), true
}

// lift removes a Player's session from every Television after
// sessionLiftDelay, unless a session of the same Player starts first.
// A lift that is still waiting at operator shutdown never runs, and the
// next operator adopts the session.
func (t *televisionSessions) lift(player string) {
	if t == nil {
		return
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()
	// A saved event of the ended session must not run again.
	delete(t.retries, player)
	t.schedule(player, t.liftDelay)
}

// schedule starts the timer of a Player's lift, and stops any earlier
// one. When the removal fails, the timer schedules it again after
// liftRetry. The caller holds the mutex.
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
	t.lifts[player] = pendingLift{timer: timer, display: t.lastDisplay[player]}
}

// remove deletes a Player's session from every Television and returns
// whether every write succeeded. The caller holds the mutex.
func (t *televisionSessions) remove(player string) bool {
	list, err := t.list()
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing Televisions to remove the session of Player %s: %v\n", player, err)
		return false
	}
	removed := true
	for _, television := range namedBy(list.Items, player) {
		removed = t.write(television.Metadata.Name, nil) && removed
	}
	return removed
}

// stop runs at operator shutdown and drops every waiting lift and saved
// retry. It cancels ctx before it takes the mutex, because a write that
// is waiting after a 429 holds the mutex. Then stop waits only for a
// request already sent, which the shared client's request timeout
// bounds.
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

// write applies one session, or removes it when session is nil, logs a
// failure, and returns whether the write succeeded. The caller holds
// the mutex.
func (t *televisionSessions) write(name string, session *TelevisionSession) bool {
	if err := t.apply(name, session); err != nil {
		fmt.Fprintf(os.Stderr, "writing the session of Television %s: %v\n", name, err)
		return false
	}
	return true
}

// namedBy returns the Televisions whose status.session is for a Player.
func namedBy(list []Television, player string) []Television {
	return slices.DeleteFunc(slices.Clone(list), func(television Television) bool {
		return television.Status.Session == nil || television.Status.Session.Player != player
	})
}

// televisionShowing returns the Television whose status.displays lists
// a Display, or nil when none does. When several Televisions of one bus
// list it, televisionFor chooses among them. Only the adapter that
// names the Display announces its physical address, so the Display
// should be on one bus. When a spec puts it on two, the first bus by
// name wins.
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
