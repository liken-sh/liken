package main

// Focus is which of a controller's units holds it right now. A Remote
// that two active Plays name has a translator sidecar in each of their
// pods, and both read its one events topic, so one press would reach two
// films. A retained mark per controller names the one Play that owns it,
// and every translator gates on the mark. The operator writes the mark:
// only it reads every Player and every Play, so only it holds a
// controller's full set of bound-and-active Plays and their order.
//
// This file is the operator's side of that. The focus desk is the
// boundary between the bus and the reconcile loop, the way the report
// desk is for a Play's status. The operator is the only writer of a
// mark, so it reads its own retained writes back and recovers the
// current marks after a restart, and the loop drains and arbitrates the
// cycle requests a source press leaves on the desk.

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

// setMark stores the owning Play for one controller. An empty play
// deletes the key, the shape a cleared retained mark arrives in.
func (f *focusDesk) setMark(key, play string) {
	f.mutex.Lock()
	if play == "" {
		delete(f.marks, key)
	} else {
		f.marks[key] = play
	}
	f.mutex.Unlock()
}

// markFor reads one controller's current mark.
func (f *focusDesk) markFor(key string) string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.marks[key]
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

// stealFocus marks each of a fresh Play's controllers to that Play. When
// a Play starts on a unit a controller drives, the most recent film takes
// the controller, which is the friendly default: start a film, and the
// controller in your hand drives it. A source press moves the mark the
// other way.
func (o *operator) stealFocus(play *Play, remotes []boundRemote) {
	namespace := play.Metadata.Namespace
	for _, remote := range remotes {
		o.publishFocus(controllerKey(namespace, remote.Name), play.Metadata.Name)
	}
}

// reconcileFocus arbitrates focus from the whole graph. It advances a
// controller one step on a cycle request, and it recovers a mark left on
// a Play that finished. Both need the full set of a controller's
// bound-and-active Plays, which only the operator holds, so both run
// here on the pass that already read every Play and Player.
func (o *operator) reconcileFocus(plays []Play, players []Player) {
	byName := map[string]*Player{}
	for index := range players {
		player := &players[index]
		byName[runKey(player.Metadata.Namespace, player.Metadata.Name)] = player
	}

	// active maps each controller to the Plays that are bound to it and
	// not finished, sorted by name so the cycle steps in the same order
	// every pass.
	active := map[string][]string{}
	for index := range plays {
		play := &plays[index]
		if terminalPhase(play.Status.Phase) {
			continue
		}
		for _, playerName := range play.Spec.Players {
			player, ok := byName[runKey(play.Metadata.Namespace, playerName)]
			if !ok {
				continue
			}
			for _, entry := range player.Spec.Remotes {
				key := controllerKey(play.Metadata.Namespace, entry.Name)
				active[key] = append(active[key], play.Metadata.Name)
			}
		}
	}
	for key := range active {
		sort.Strings(active[key])
	}

	// A cycle advances one step from the current mark. A mark that names
	// no active Play reads as index -1, so the step lands on the first,
	// and the modulo wraps the last back to the first.
	for _, key := range o.focus.takeCycles() {
		names := active[key]
		if len(names) == 0 {
			continue
		}
		index := slices.Index(names, o.focus.markFor(key))
		o.publishFocus(key, names[(index+1)%len(names)])
	}

	// Recovery moves a mark off a Play that finished, so a controller with
	// a live Play always has a translator that acts. It never moves a mark
	// that still names an active Play, so it does not steal from a holder.
	for key, names := range active {
		current := o.focus.markFor(key)
		if current == "" || !slices.Contains(names, current) {
			o.publishFocus(key, names[0])
		}
	}
}

// publishFocus writes one controller's mark to the retained topic and to
// the local desk, the two the operator keeps in step so the cycle math
// on the same pass reads the value it just wrote.
func (o *operator) publishFocus(key, play string) {
	namespace, remote, _ := strings.Cut(key, "/")
	o.bus.Publish(remoteFocusTopic(o.topicBase, namespace, remote), []byte(play), true)
	o.focus.setMark(key, play)
}
