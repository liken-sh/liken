package main

// A unit's Receiver match holds through a short gap in its screen.
//
// The screen resolves from the idle claim's allocation, and the claim
// loses its allocation while the pod that holds it is replaced: the old
// pod is gone, and the scheduler has not placed the new one. A match
// that followed the claim through that gap would lift the session on
// the equipment and drop the power topic from status.idle.bus. A
// delegate that builds the power topic into its pod's template then
// replaces the pod again, the claim loses its allocation again, and the
// loop turns the equipment off and on every pass until a pass finds
// the claim allocated before the delegate reads the status. So the pass keeps the last screen a unit resolved for
// screenGapBound, and matches the Receivers against that screen.
//
// The pass matches the held screen against the Receivers it reads now,
// so a person who rewires a Receiver during a gap still moves the
// session.

import "time"

// screenGapBound is how long a unit keeps its last screen after the
// claim loses its allocation.
//
// The scheduler writes the allocation when it binds the new pod, before
// the kubelet pulls the image, so a slow pull does not make the gap
// longer. On a home cluster, the pass after a pod replacement found
// the claim allocated again 20 to 110 ms after the pass that found it
// unallocated. The gap is longer in two cases: the
// scheduler retries a pod it could not place with a backoff that grows
// to 10 s, and a delegate operator that stops between its delete and
// its create leaves the create to a new leader, which takes over within
// a 30-second Lease. 90 s covers both. A screen that is really gone
// keeps its session for up to 90 s more, and then the pass lifts it.
const screenGapBound = 90 * time.Second

// heldScreen is the last screen one unit resolved in this run. lostAt
// is zero while the screen resolves, and is the moment the pass first
// found it unresolved.
type heldScreen struct {
	screen screen
	lostAt time.Time
}

// matchedScreen answers the screen a unit's Receiver match reads: the
// screen the claim resolves to now, or the held one while the gap is
// inside the bound. A unit with no screen held in this run has none to
// keep, so an operator that starts during a gap matches nothing until
// the claim is allocated again.
//
// The first pass of a gap schedules one wake at the bound, so the pass
// that lifts the session runs when the bound ends and not at the next
// unrelated event.
func (o *operator) matchedScreen(player *Player) (screen, bool) {
	key := playerKey(player.Metadata.Namespace, player.Metadata.Name)
	if found, resolved := o.screenLookup().screenFor(player); resolved {
		o.heldScreens[key] = heldScreen{screen: found}
		return found, true
	}
	held, holds := o.heldScreens[key]
	if !holds {
		return screen{}, false
	}
	now := o.now()
	if held.lostAt.IsZero() {
		held.lostAt = now
		o.heldScreens[key] = held
		o.requeueAfter(screenGapBound)
	}
	if now.Sub(held.lostAt) >= screenGapBound {
		delete(o.heldScreens, key)
		return screen{}, false
	}
	return held.screen, true
}

// retainHeldScreens drops the held screen of every unit the cluster no
// longer holds, so a Player created again under the same name starts
// with no screen to keep.
func (o *operator) retainHeldScreens(live map[string]bool) {
	for key := range o.heldScreens {
		if !live[key] {
			delete(o.heldScreens, key)
		}
	}
}
