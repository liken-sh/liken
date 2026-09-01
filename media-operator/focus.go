package main

// Focus is which unit holds a controller's presses right now. One
// controller can drive several units, so its one events topic has several
// readers: a translator in every Play's pod, and the idle command pod of every
// unit that lists it. Without a gate, one press would reach them all. A
// retained mark per Remote names the one Player that owns the presses, and
// every reader gates on the mark. The mark names a Player and never a Play:
// a claim is exclusive, so one active Play holds one Player, and the film
// on the focused Player is unambiguous. A mark may also name an idle
// Player, which is how the idle screen takes presses at all.
//
// This file is the operator's side of that. The focus desk is the boundary
// between the bus and the reconcile loop, the way the report desk is for a
// Play's status. The operator is the only writer of a mark: only it reads
// every Player, so only it holds a controller's full cycle set and its
// order. It reads its own retained writes back, so it recovers the current
// marks after a restart, and the loop drains and arbitrates the cycle
// requests a source press leaves on the desk.

import (
	"slices"
	"sort"
	"strings"
	"sync"
)

// focusDesk holds the current mark per controller and the pending cycle
// requests. One mutex guards both, because the bus handler writes them on
// its goroutine and the reconcile loop reads and clears them on its own.
type focusDesk struct {
	mutex  sync.Mutex
	marks  map[string]string
	cycles map[string]bool
	wake   chan<- struct{}
}

func newFocusDesk(wake chan<- struct{}) *focusDesk {
	return &focusDesk{
		marks:  map[string]string{},
		cycles: map[string]bool{},
		wake:   wake,
	}
}

// setMark stores the owning Player for one controller. An empty player
// deletes the key, the shape a cleared retained mark arrives in. A value
// that changes wakes the loop, because a Player's bus status and a Remote's
// status both derive from the mark, and the pass that writes them must run
// again. The operator reads its own retained writes back, and those repeat
// the stored value, so they wake nothing.
func (f *focusDesk) setMark(key, player string) {
	f.mutex.Lock()
	was := f.marks[key]
	if player == "" {
		delete(f.marks, key)
	} else {
		f.marks[key] = player
	}
	f.mutex.Unlock()
	if was != player {
		poke(f.wake)
	}
}

// markFor reads one controller's current mark.
func (f *focusDesk) markFor(key string) string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.marks[key]
}

// snapshot copies the current marks, so the operator republishes them
// after a fresh broker session without holding the desk lock while it
// writes the bus.
func (f *focusDesk) snapshot() map[string]string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	marks := make(map[string]string, len(f.marks))
	for key, player := range f.marks {
		marks[key] = player
	}
	return marks
}

// requestCycle records that a source press asked to cycle one controller
// and wakes the loop to arbitrate it. The cycle is an event, so a request
// the operator misses while it is down is lost, and a later press asks
// again.
func (f *focusDesk) requestCycle(key string) {
	f.mutex.Lock()
	f.cycles[key] = true
	f.mutex.Unlock()
	poke(f.wake)
}

// takeCycles returns the controllers with a pending cycle request and
// clears the set, so the loop arbitrates each request once.
func (f *focusDesk) takeCycles() []string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	keys := make([]string, 0, len(f.cycles))
	for key := range f.cycles {
		keys = append(keys, key)
	}
	f.cycles = map[string]bool{}
	return keys
}

// controllerKey is the one key shape for a controller, its namespace and
// name. It matches the two segments the focus topic path carries, so the
// bus handler and the loop name a controller the same way.
func controllerKey(namespace, remote string) string {
	return namespace + "/" + remote
}

// stealFocus marks each of a fresh Play's controllers to the Player the
// Play runs on. When a Play starts on a unit a controller drives, the
// newest film takes the controller, which is the friendly default: start a
// film, and the controller in your hand drives it. A source press moves
// the mark the other way.
func (o *operator) stealFocus(play *Play, remotes []boundRemote) {
	namespace := play.Metadata.Namespace
	for _, remote := range remotes {
		o.publishFocus(controllerKey(namespace, remote.Name), playerName(play))
	}
}

// reconcileFocus arbitrates focus from the Players alone. A controller's
// cycle set is every Player in its namespace whose spec.remotes lists it,
// in name order, so the cycle steps the same way every pass and an idle
// Player is a real stop. No Play is read here: a claim is exclusive, so the
// film on the focused Player is whatever Play holds its claim, and a Play
// that finishes moves no mark, because the Player it names is still there,
// showing its idle screen.
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

	// A cycle advances one step from the current mark. A mark that names no
	// Player in the set reads as index -1, so the step lands on the first,
	// and the modulo wraps the last back to the first. A set of one wraps
	// onto itself and republishes the same mark, and that repeat is the
	// press's feedback: the idle screen answers it with a pulse on its
	// focus marker.
	for _, key := range o.focus.takeCycles() {
		names := sets[key]
		if len(names) == 0 {
			continue
		}
		index := slices.Index(names, o.focus.markFor(key))
		o.publishFocus(key, names[(index+1)%len(names)])
	}

	// Recovery moves a mark that is empty or names a Player outside the
	// set, so a controller always drives a unit that lists it. It never
	// moves a mark that still names a Player in the set, so it does not
	// steal from a holder.
	for key, names := range sets {
		current := o.focus.markFor(key)
		if current == "" || !slices.Contains(names, current) {
			o.publishFocus(key, names[0])
		}
	}

	// A controller no Player lists any more has nothing to drive, so its
	// mark is cleared. The empty retained payload is the delete, and it
	// leaves no stale mark on the bus for a later reader to gate open on.
	for key := range o.focus.snapshot() {
		if len(sets[key]) == 0 {
			o.publishFocus(key, "")
		}
	}
}

// publishFocus writes one controller's mark to the retained topic and to
// the local desk, the two the operator keeps in step so the cycle math on
// the same pass reads the value it just wrote. It publishes every time it
// is called, an unchanged value included, because the repeat of a current
// mark is the feedback a cycle press earns.
func (o *operator) publishFocus(key, player string) {
	namespace, remote, _ := strings.Cut(key, "/")
	o.bus.Publish(remoteFocusTopic(o.topicBase, namespace, remote), []byte(player), true)
	o.focus.setMark(key, player)
}
