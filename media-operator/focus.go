package main

// Focus decides which Player receives a controller's key presses. One
// controller can drive several Players, so its events topic has several
// readers: the command sidecar in each Play's pod and the idle screen
// of each Player that lists the controller. Each Remote has a retained
// focus mark on the bus, which is the name of one Player, and every
// reader ignores presses unless the mark names its own Player. The mark
// names a Player and not a Play, because a Player's claim is exclusive,
// so a Player runs at most one Play. A mark can name an idle Player,
// and then the idle screen receives the presses.
//
// The operator is the only process that publishes marks, because it
// reads every Player and so can compute which Players a controller
// cycles through. After a restart it reads its own retained marks back
// from the bus. The bus handler stores marks and cycle requests in a
// focusCache, and the reconcile loop reads them from it.

import (
	"slices"
	"sort"
	"strings"
	"sync"
)

// focusCache stores the current mark of each controller and the pending
// cycle requests. The bus handler writes them on its goroutine and the
// reconcile loop reads and clears them on another, so one mutex guards
// all three maps.
//
// confirmed has each controller whose mark the broker retains in the
// current session, because the broker sent it on this session or the
// operator published it on this session. After a reconnect, a stored
// mark that is not confirmed is one that a restarted broker lost, and
// the operator publishes only those marks again. Each publish reaches
// every reader as a live message that no person caused.
type focusCache struct {
	mutex     sync.Mutex
	marks     map[string]string
	confirmed map[string]bool
	cycles    map[string]bool
	wake      chan<- struct{}
}

func newFocusCache(wake chan<- struct{}) *focusCache {
	return &focusCache{
		marks:     map[string]string{},
		confirmed: map[string]bool{},
		cycles:    map[string]bool{},
		wake:      wake,
	}
}

// newSession clears confirmed. The bus calls it on each connect, before
// the new session delivers any message.
func (f *focusCache) newSession() {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.confirmed = map[string]bool{}
}

// unconfirmed returns a copy of the stored marks that the current
// session has not confirmed.
func (f *focusCache) unconfirmed() map[string]string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	marks := map[string]string{}
	for key, player := range f.marks {
		if !f.confirmed[key] {
			marks[key] = player
		}
	}
	return marks
}

// setMark stores the Player that receives one controller's presses,
// for a mark that arrives from the bus and for a mark that the operator
// just published. Either one confirms the mark for the current session.
// An empty player deletes the mark, because a cleared retained message
// arrives with an empty payload. A changed value wakes the reconcile
// loop, because the Player's bus status and the Remote's status are
// derived from the mark. When the operator's own message comes back
// from the bus, the value is unchanged, so it wakes nothing.
func (f *focusCache) setMark(key, player string) {
	f.mutex.Lock()
	was := f.marks[key]
	if player == "" {
		delete(f.marks, key)
		delete(f.confirmed, key)
	} else {
		f.marks[key] = player
		f.confirmed[key] = true
	}
	f.mutex.Unlock()
	if was != player {
		signalWake(f.wake)
	}
}

// markFor returns the Player that receives one controller's presses,
// or an empty string.
func (f *focusCache) markFor(key string) string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.marks[key]
}

// snapshot returns a copy of the marks, so the reconcile loop does not
// hold the mutex while it publishes to the bus.
func (f *focusCache) snapshot() map[string]string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	marks := make(map[string]string, len(f.marks))
	for key, player := range f.marks {
		marks[key] = player
	}
	return marks
}

// requestCycle records that a press asked to move one controller to the
// next Player, and wakes the reconcile loop to do it. A request made
// while the operator is down is lost, and the next press asks again.
func (f *focusCache) requestCycle(key string) {
	f.mutex.Lock()
	f.cycles[key] = true
	f.mutex.Unlock()
	signalWake(f.wake)
}

// takeCycles returns the controllers with a pending cycle request and
// clears them, so the reconcile loop handles each request once.
func (f *focusCache) takeCycles() []string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	keys := make([]string, 0, len(f.cycles))
	for key := range f.cycles {
		keys = append(keys, key)
	}
	f.cycles = map[string]bool{}
	return keys
}

// controllerKey is the key of a Remote in focusCache. The namespace and
// the name are the same two segments that the Remote's focus topic has.
func controllerKey(namespace, remote string) string {
	return namespace + "/" + remote
}

// takeFocus sets the mark of each of a new Play's controllers to the
// Player that the Play runs on, so a person who starts a film controls
// it with the controller in their hand. A cycle press can move the mark
// away again.
func (o *operator) takeFocus(play *Play, remotes []boundRemote) {
	namespace := play.Metadata.Namespace
	for _, remote := range remotes {
		o.publishFocus(controllerKey(namespace, remote.Name), playerName(play),
			"play "+namespace+"/"+play.Metadata.Name+" started there")
	}
}

// reconcileFocus applies cycle requests and repairs marks from the
// Players alone. A controller's cycle set is the Players in its
// namespace whose spec.remotes lists it, sorted by name, so a cycle
// visits them in the same order each time, idle Players included. It
// reads no Play, because a finished Play leaves its Player on the idle
// screen, and the mark stays correct.
func (o *operator) reconcileFocus(players []Player) {
	sets := map[string][]string{}
	for index := range players {
		player := &players[index]
		for _, entry := range player.Spec.Remotes {
			key := controllerKey(player.Metadata.Namespace, entry.Name)
			sets[key] = append(sets[key], player.Metadata.Name)
		}
	}
	for key := range sets {
		sort.Strings(sets[key])
	}

	// A mark that names no Player in the set gives index -1, so the
	// cycle starts at the first Player. With one Player in the set, the
	// cycle publishes the same mark again. The idle screen that sent the
	// cycle pulses its focus marker when the repeat arrives, so the
	// person sees that the press worked.
	for _, key := range o.focus.takeCycles() {
		names := sets[key]
		if len(names) == 0 {
			logLine(o.log, "remote %s: asked to cycle focus, ignored, because no player lists the remote", key)
			continue
		}
		index := slices.Index(names, o.focus.markFor(key))
		next := names[(index+1)%len(names)]
		if len(names) == 1 {
			logLine(o.log, "remote %s: asked to cycle focus, focus stays on player %s, the only Player that lists the Remote", key, next)
		}
		o.publishFocus(key, next, "the remote asked to cycle focus")
	}

	// A restarted broker has lost its retained marks. Once caughtUp is
	// true, publish again each stored mark that the new session did not
	// confirm. Skip a mark that names a Player outside the set, because
	// the recovery below replaces it.
	if o.caughtUp() {
		for key, player := range o.focus.unconfirmed() {
			if slices.Contains(sets[key], player) {
				o.publishFocus(key, player, "the broker session is new and the broker holds no mark")
			}
		}
	}

	// Move a mark that is empty or names a Player outside the set to the
	// first Player of the set, so a controller always drives a Player
	// that lists it. A mark that names a Player in the set stays.
	//
	// Before caughtUp is true, an empty mark does not prove that the
	// broker has no mark, because the retained mark can still be on its
	// way. Recovering then would
	// move a controller that several Players share to the first one.
	for key, names := range sets {
		current := o.focus.markFor(key)
		if current == "" && !o.caughtUp() {
			continue
		}
		if current == "" || !slices.Contains(names, current) {
			o.publishFocus(key, names[0], "the mark named no player that lists the remote")
		}
	}

	// Clear the mark of a controller that no Player lists. An empty
	// retained payload deletes the broker's retained message, so a reader
	// that subscribes later does not receive a stale mark.
	for key := range o.focus.snapshot() {
		if len(sets[key]) == 0 {
			o.publishFocus(key, "", "no player lists the remote")
		}
	}
}

// publishFocus publishes one controller's mark as a retained message
// and stores it in the focusCache, so the rest of the same pass reads the
// new value. It publishes even when the value is unchanged, because the
// repeat is the feedback for a cycle press. It logs only a change. The
// caller logs a cycle that stays on the same Player.
func (o *operator) publishFocus(key, player, reason string) {
	namespace, remote, _ := strings.Cut(key, "/")
	topic := remoteFocusTopic(o.topicBase, namespace, remote)
	was := o.focus.markFor(key)
	o.bus.Publish(topic, []byte(player), true)
	o.focus.setMark(key, player)
	switch {
	case was == player:
	case player == "":
		logLine(o.log, "remote %s: focus cleared from player %s, because %s, published the empty mark to %s", key, was, reason, topic)
	case was == "":
		logLine(o.log, "remote %s: focus set on player %s, because %s, published to %s", key, player, reason, topic)
	default:
		logLine(o.log, "remote %s: focus moved from player %s to player %s, because %s, published to %s", key, was, player, reason, topic)
	}
}
