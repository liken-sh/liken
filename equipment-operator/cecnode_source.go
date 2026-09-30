package main

// The active source as one adapter knows it: the last Active Source it
// heard or sent, and the claim a wake makes and guards. CEC has no
// query for the TV's input, so the last Active Source on the wire is
// the only report of it the bus gives. A TV that switches to its own
// apps, or to an input with no CEC, sends nothing, so this is the last
// CEC source and not what the TV shows.

import (
	"context"
	"fmt"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
)

// noteSource records the physical address of an Active Source the
// adapter heard or sent, asks the loop to write the entry when it
// changed, and wakes a wake that guards its claim. An Active Source for
// 0.0.0.0 comes from the TV alone, when a person switches it to its own
// tuner or apps (HDMI-CEC 1.3a, CEC 13.2.2), so it is a person's
// choice of route. So is the Active Source of the source a person's
// route leads to, which that source sends to answer the choice.
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
	poke(n.sources)
}

// currentSource answers the last Active Source the adapter knows.
func (n *cecNode) currentSource() cec.PhysicalAddress {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	return n.source
}

// claimInput sends the TV Image View On and then broadcasts Active
// Source for a physical address, and records the claim. HDMI-CEC 1.3a,
// CEC 13.1.2, says a source that needs its output on the screen sends
// Image View On whenever it sends Active Source, because the source
// does not know whether the TV is in standby. A TV that shows its own
// apps also switches on that pair, and not on a bare Active Source. So
// every claim the adapter makes for a person comes through here: the
// wake's claims and a home press. An answer to a question the bus
// asked is Active Source alone (announceSource). It answers the name
// of the message that failed, with the error.
func (n *cecNode) claimInput(own cec.LogicalAddress, physical cec.PhysicalAddress) (string, error) {
	if _, err := n.device.Transmit(cec.ImageViewOn(own, cec.AddressTV), 0, 0); err != nil {
		return "Image View On", err
	}
	return n.announceSource(own, physical)
}

// announceSource broadcasts Active Source for a physical address and
// records it. The kernel does not pass an adapter its own
// transmission, so the adapter records its own claim, and a wake's
// guard sees it.
func (n *cecNode) announceSource(own cec.LogicalAddress, physical cec.PhysicalAddress) (string, error) {
	if _, err := n.device.Transmit(cec.ActiveSource(own, physical), 0, 0); err != nil {
		return "Active Source for " + physical.String(), err
	}
	n.noteSource(physical)
	return "", nil
}

// claimSource sends Image View On and Active Source for the adapter's
// own physical address, and guards the claim for cecWakeGuard: when
// another source claims the input, the adapter waits cecWakeSettle and
// sends the pair again, at most cecWakeReclaims times. The claims come from the
// wake's own budget, so a wake that runs again sends no more of them.
// An Active Source from another adapter of the bus is a later wake of
// the bus, so the guard ends at once and claims nothing more. The
// verdict is what the bus reports when the guard ends: whether the
// last Active Source is the adapter's own.
//
// The guard takes the input back only from a bare Active Source of
// another source device. Plan 09 measured the case it exists for: a
// streaming player that wakes with the room sends Active Source by
// itself, and the TV and the receiver follow it. A route that a person
// moves ends the guard with no claim, because the adapter claims the
// input for a person and never against one. A person moves the route
// through the TV's menu, which sends Set Stream Path; through a
// switch's front panel, which sends Routing Change (HDMI-CEC 1.3a, CEC
// 13.2.2); through the TV's own tuner or apps, which send Active Source
// for 0.0.0.0; and through the TV's power button, which sends Standby.
// Any other move of the route, such as a switch's Routing Information,
// neither ends the guard nor asks for a claim: only another source's
// Active Source is a claim to take back.
//
// A wake that answers the TV's own Set Stream Path for the Display
// (cecnode_pick.go) sends Active Source alone, in its first claim and in
// each claim after: the TV is on and already shows the Display's input,
// so Image View On has nothing to ask of it.
func (n *cecNode) claimSource(ctx context.Context, job *wakeJob, own cec.LogicalAddress, physical cec.PhysicalAddress, display string) powerResult {
	drainPokes(n.sources)
	claim := claimWords{pair: !n.takePick(physical)}
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
			return &powerResult{stopped: true, log: fmt.Sprintf("the adapter on %s left while it sent %s: %v", n.machine, failed, err)}
		case err != nil:
			message := fmt.Sprintf("the adapter on %s could not send %s: %v", n.machine, failed, err)
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
		if route.leadsTo(physical) || (!route.chosen && route.by != cec.OpActiveSource) {
			continue
		}
		if route.chosen {
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
		route, _ = n.guardView()
		if route.leadsTo(physical) || (!route.chosen && route.by != cec.OpActiveSource) {
			continue
		}
		if route.chosen {
			return chosenVerdict(n.machine, claim, physical, display, sends, route, time.Since(began))
		}
		if ended := send(); ended != nil {
			return *ended
		}
	}
	return sourceVerdict(n.machine, claim, physical, display, n.currentSource(), sends, taken, takenBy, takenAfter)
}

// claimWords names what each claim of one wake sends: Image View On
// and Active Source, or Active Source alone for a wake that answers the
// TV's Set Stream Path.
type claimWords struct {
	pair bool
}

func (c claimWords) String() string {
	if c.pair {
		return "Image View On and Active Source"
	}
	return "Active Source"
}

// guardView answers the route and the last Active Source as the
// adapter holds them now, for a guard that a message woke.
func (n *cecNode) guardView() (routeState, cec.PhysicalAddress) {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	return n.route, n.source
}

// chosenVerdict states a guard that a person's choice of route ended.
func chosenVerdict(machine string, claim claimWords, physical cec.PhysicalAddress, display string, sends int, route routeState, after time.Duration) powerResult {
	message := fmt.Sprintf("the adapter on %s sent %s for %s, Display %s, %s, and %s after %s moved the picture away from the Display, which a person does through the TV or a switch, so the guard ended and the adapter claims nothing more",
		machine, claim, physical, display, times(sends), route.words, elapsed(after))
	return powerResult{verdict: verdict{ConditionFalse, reasonChosen, message}, log: message}
}

// sourceVerdict states what the bus reports at the end of a guard.
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
