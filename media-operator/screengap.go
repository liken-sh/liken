package main

// A Player's screen comes from the allocation of its idle claim. The
// claim has no allocation while the pod that holds it is replaced,
// between the delete of the old pod and the scheduling of the new one.
// If the Receiver match followed the claim through that gap, the pass
// would remove the session from the Receiver and write it again after
// the gap, and the equipment would turn off and on each time a screen
// pod is replaced. A press during the gap would also not switch the
// Receiver's input, and a Television's screen request would not be
// relayed. So for up to screenGapBound the pass matches the Receivers
// against the last screen that the Player resolved.
//
// The pass matches that screen against the Receivers as it reads them
// now, so rewiring a Receiver during a gap still moves the session.

import "time"

// screenGapBound is how long a Player keeps its last screen after the
// idle claim loses its allocation.
//
// The scheduler writes the allocation when it binds the new pod, before
// the kubelet pulls the image, so a slow pull does not lengthen the gap.
// The gap is longer when the scheduler retries a pod it could not place,
// with a backoff of up to 10 s, and when another operator that replaces
// the screen pod stops between its delete and its create, so that a new
// leader makes the create after its 30-second Lease. The cost is that a
// screen that is really gone keeps its Receiver session for up to 90 s.
const screenGapBound = 90 * time.Second

// lastScreen is the last screen that one Player resolved since this
// operator started. lostAt is zero while the screen resolves, and
// otherwise the time the pass first found it unresolved.
type lastScreen struct {
	screen screen
	lostAt time.Time
}

// matchedScreen returns the screen to match a Player's Receivers
// against: the screen the claim resolves to now, or the last one while
// the gap is shorter than screenGapBound. The last screen is kept only
// in memory, so an operator that starts during a gap matches nothing
// until the claim is allocated again.
//
// The first pass of a gap requeues a pass at the end of the bound, so
// the session is removed then and not at the next unrelated event.
func (o *operator) matchedScreen(player *Player) (screen, bool) {
	key := playerKey(player.Metadata.Namespace, player.Metadata.Name)
	if found, resolved := o.screenLookup().screenFor(player); resolved {
		o.lastScreens[key] = lastScreen{screen: found}
		return found, true
	}
	last, ok := o.lastScreens[key]
	if !ok {
		return screen{}, false
	}
	now := o.now()
	if last.lostAt.IsZero() {
		last.lostAt = now
		o.lastScreens[key] = last
		o.requeueAfter(screenGapBound)
	}
	if now.Sub(last.lostAt) >= screenGapBound {
		delete(o.lastScreens, key)
		return screen{}, false
	}
	return last.screen, true
}

// retainHeldScreens forgets the screen of every Player that is not in
// live, so a Player that is deleted and created again under the same
// name does not match through a gap it never had.
func (o *operator) retainHeldScreens(live map[string]bool) {
	for key := range o.lastScreens {
		if !live[key] {
			delete(o.lastScreens, key)
		}
	}
}
