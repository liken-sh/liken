package main

// The node workload's record of the active source on its adapter's
// bus, and the claim of the input that a wake sends and then guards.
// CEC has no message that asks the TV for its current input, so the
// last Active Source on the bus is the only evidence of it. A TV can
// switch to its own apps, or to an input without CEC, without sending
// Active Source, so the recorded source can differ from what the TV
// shows.

import (
	"context"
	"fmt"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

// noteSource records the physical address of an Active Source that the
// adapter received or sent, and moves the route to it. It marks the
// status entry for writing when the address changed, and signals
// n.sources so that a wake's guard checks the route.
//
// An Active Source for 0.0.0.0 comes only from the TV, when a person
// switches it to its own tuner or apps (HDMI-CEC 1.3a, CEC 13.2.2), so
// that route counts as a person's choice. When a person chose a route
// and the source at that address then sends Active Source, noteSource
// keeps the route as the person's choice, so the guard does not take
// that input back.
func (n *cecNode) noteSource(address cec.PhysicalAddress) {
	n.mutex.Lock()
	changed := n.source != address
	n.source = address
	route := routeState{address: address, known: true, chosen: address == 0, by: cec.OpActiveSource, words: "Active Source for " + address.String()}
	if n.route.chosen && n.route.address == address {
		route = n.route
	}
	n.route = route
	n.mutex.Unlock()
	if changed {
		n.markDirty()
	}
	sendWake(n.sources)
}

// currentSource returns the address of the last Active Source that the
// adapter received or sent.
func (n *cecNode) currentSource() cec.PhysicalAddress {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	return n.source
}

// claimInput sends Image View On to the TV and then broadcasts Active
// Source for physical. HDMI-CEC 1.3a, CEC 13.1.2, says a source that
// needs the screen sends Image View On with each Active Source, because
// it cannot know whether the TV is in standby. A TV that shows its own
// apps switches input on that pair but not on Active Source alone. So a
// wake and a home press claim the input here, and a reply to another
// device's request calls announceSource. On a failure it returns the
// name of the message that failed, with the error.
func (n *cecNode) claimInput(own cec.LogicalAddress, physical cec.PhysicalAddress) (string, error) {
	if _, err := n.device.Transmit(cec.ImageViewOn(own, cec.AddressTV), 0, 0); err != nil {
		return "Image View On", err
	}
	return n.announceSource(own, physical)
}

// announceSource broadcasts Active Source for physical and records it
// with noteSource. The kernel does not deliver an adapter's own
// messages back to it, so without this record a wake's guard would not
// see its own claim. On a failure it returns the name of the message
// that failed, with the error.
func (n *cecNode) announceSource(own cec.LogicalAddress, physical cec.PhysicalAddress) (string, error) {
	if _, err := n.device.Transmit(cec.ActiveSource(own, physical), 0, 0); err != nil {
		return "Active Source for " + physical.String(), err
	}
	n.noteSource(physical)
	return "", nil
}

// claimSource claims the input for the adapter's own physical address,
// then guards the claim for cecWakeGuard. When another source sends
// Active Source, the adapter waits cecWakeSettle and claims again, up
// to cecWakeReclaims more times. n.woken.claims counts the sends of
// each wake, so a wake that runs again does not start the count over.
//
// The guard takes the input back from a source that claims it without
// a person, such as a streaming player that wakes with the room. It
// never takes the input back from a person. A Set Stream Path from the
// TV's menu, a Routing Change from a switch's front panel (HDMI-CEC
// 1.3a, CEC 13.2.2), an Active Source for 0.0.0.0 from the TV's own
// tuner or apps, and a Standby each end the guard with reasonChosen.
// guardMove makes the exception for the TV's own Set Stream Path just
// after the first claim. An Active Source from another adapter on the
// bus means a later wake there, and ends the guard with
// reasonSuperseded.
//
// When the TV itself just selected the Display (consumeStreamPath), each claim
// is Active Source alone, because the TV is on and already shows that
// input.
func (n *cecNode) claimSource(ctx context.Context, job *wakeJob, own cec.LogicalAddress, physical cec.PhysicalAddress, display string) powerResult {
	drainWakes(n.sources)
	claim := claimWords{pair: !n.consumeStreamPath(physical)}
	sends := 0
	send := func() *powerResult {
		n.mutex.Lock()
		budget := &n.woken.claims
		if budget.sent != job.key {
			budget.sent, budget.sends = job.key, 0
		}
		spent := budget.sends > cecWakeReclaims
		if !spent {
			budget.sends++
		}
		n.mutex.Unlock()
		if spent {
			return nil
		}
		var failed string
		var err error
		if claim.pair {
			failed, err = n.claimInput(own, physical)
		} else {
			failed, err = n.announceSource(own, physical)
		}
		switch {
		case err != nil && cec.IsGone(err):
			n.fail(err)
			return &powerResult{stopped: true, log: fmt.Sprintf("the adapter on %s was removed while it sent %s: %v", n.machine, failed, err)}
		case err != nil:
			message := fmt.Sprintf("the adapter on %s did not send %s: %v", n.machine, failed, err)
			return &powerResult{verdict: verdict{ConditionFalse, reasonRefused, message}, log: message}
		}
		sends++
		job.claimed.Store(true)
		return nil
	}
	if ended := send(); ended != nil {
		return *ended
	}
	began := time.Now()
	stopped := func() powerResult {
		return powerResult{stopped: true, log: fmt.Sprintf("the adapter on %s sent %s for %s %s, and the wake stopped after %s", n.machine, claim, physical, times(sends), elapsed(time.Since(began)))}
	}
	guard := time.NewTimer(cecWakeGuard)
	defer guard.Stop()
	taken, takenBy, takenAfter := 0, cec.InvalidPhysicalAddress, time.Duration(0)
	for guarding := true; guarding; {
		select {
		case <-ctx.Done():
			return stopped()
		case <-guard.C:
			guarding = false
			continue
		case <-n.sources:
		}
		route, holder := n.guardView()
		switch guardMove(route, holder, physical, time.Since(began)) {
		case moveNone:
			continue
		case moveChosen:
			return chosenVerdict(n.machine, claim, physical, display, sends, route, time.Since(began))
		}
		if machine, other := job.others[holder]; other {
			message := fmt.Sprintf("the adapter on %s sent %s for %s, Display %s, %s, and the adapter on %s sent Active Source for %s after %s, so the guard ended",
				n.machine, claim, physical, display, times(sends), machine, holder, elapsed(time.Since(began)))
			return powerResult{verdict: verdict{ConditionFalse, reasonSuperseded, message}, log: message}
		}
		if taken == 0 {
			takenAfter = time.Since(began)
		}
		taken, takenBy = taken+1, holder
		if sends > cecWakeReclaims {
			continue
		}
		select {
		case <-ctx.Done():
			return stopped()
		case <-time.After(cecWakeSettle):
		}
		route, holder = n.guardView()
		switch guardMove(route, holder, physical, time.Since(began)) {
		case moveNone:
			continue
		case moveChosen:
			return chosenVerdict(n.machine, claim, physical, display, sends, route, time.Since(began))
		}
		if ended := send(); ended != nil {
			return *ended
		}
	}
	return sourceVerdict(n.machine, claim, physical, display, n.currentSource(), sends, taken, takenBy, takenAfter)
}

// guardMovement classifies one change of the route for a wake's guard.
type guardMovement int

const (
	// moveNone needs no action: the route leads to the Display, or the
	// message that moved it is not a claim.
	moveNone guardMovement = iota
	// moveChosen is a person's choice of another source. It ends the
	// guard.
	moveChosen
	// moveClaimed is another source's Active Source. The guard takes
	// the input back.
	moveClaimed
)

// guardMove classifies the current route for a wake's guard. since is
// the time since the wake's first claim. Within cecBootGrace, a Set
// Stream Path from the TV comes from the TV switching as it powers on,
// and noteSource keeps that route when a streaming player then sends
// Active Source. So in that time the guard ignores the route, and takes
// the input back when another source sent the last Active Source.
func guardMove(route routeState, holder, physical cec.PhysicalAddress, since time.Duration) guardMovement {
	if route.chosen && route.by == cec.OpSetStreamPath && since < cecBootGrace {
		if holder == physical || holder == cec.InvalidPhysicalAddress {
			return moveNone
		}
		return moveClaimed
	}
	switch {
	case route.leadsTo(physical) || (!route.chosen && route.by != cec.OpActiveSource):
		return moveNone
	case route.chosen:
		return moveChosen
	}
	return moveClaimed
}

// claimWords is what each claim of one wake sends: Image View On and
// Active Source, or Active Source alone.
type claimWords struct {
	pair bool
}

func (c claimWords) String() string {
	if c.pair {
		return "Image View On and Active Source"
	}
	return "Active Source"
}

// guardView reads the route and the last Active Source under one lock,
// so the two agree.
func (n *cecNode) guardView() (routeState, cec.PhysicalAddress) {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	return n.route, n.source
}

// chosenVerdict builds the result for a guard that ended because a
// person chose another route.
func chosenVerdict(machine string, claim claimWords, physical cec.PhysicalAddress, display string, sends int, route routeState, after time.Duration) powerResult {
	message := fmt.Sprintf("the adapter on %s sent %s for %s, Display %s, %s, and %s after %s moved the picture away from the Display, which a person does through the TV or a switch, so the guard ended and the adapter claims nothing more",
		machine, claim, physical, display, times(sends), route.words, elapsed(after))
	return powerResult{verdict: verdict{ConditionFalse, reasonChosen, message}, log: message}
}

// sourceVerdict builds the result for a guard that ran for the whole
// cecWakeGuard: reasonConfirmed when the last Active Source on the bus
// is the adapter's own, and reasonTaken when it is another source's.
func sourceVerdict(machine string, claim claimWords, physical cec.PhysicalAddress, display string, holder cec.PhysicalAddress, sends, taken int, takenBy cec.PhysicalAddress, takenAfter time.Duration) powerResult {
	message := fmt.Sprintf("the adapter on %s sent %s for %s, Display %s, %s", machine, claim, physical, display, times(sends))
	if taken == 0 {
		message += fmt.Sprintf(", and no other source claimed the input in the %s after", elapsed(cecWakeGuard))
	} else {
		message += fmt.Sprintf(", because the source at %s claimed the input %s, first after %s", takenBy, times(taken), elapsed(takenAfter))
	}
	if holder == physical {
		message += fmt.Sprintf("; the last Active Source on the bus is %s", physical)
		return powerResult{verdict: verdict{ConditionTrue, reasonConfirmed, message}, log: message}
	}
	message += fmt.Sprintf("; the source at %s holds the input, and the adapter sends the pair at most %s for one wake", holder, times(cecWakeReclaims+1))
	return powerResult{verdict: verdict{ConditionFalse, reasonTaken, message}, log: message}
}
