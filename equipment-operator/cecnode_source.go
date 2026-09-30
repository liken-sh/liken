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
// changed, and wakes a wake that guards its claim.
func (n *cecNode) noteSource(address cec.PhysicalAddress) {
	n.mutex.Lock()
	changed := n.source != address
	n.source = address
	n.route = routeState{address: address, known: true}
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
func (n *cecNode) claimSource(ctx context.Context, job *wakeJob, own cec.LogicalAddress, physical cec.PhysicalAddress, display string) powerResult {
	drainPokes(n.sources)
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
		failed, err := n.claimInput(own, physical)
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
	guard := time.NewTimer(cecWakeGuard)
	defer guard.Stop()
	taken, takenBy, takenAfter := 0, cec.InvalidPhysicalAddress, time.Duration(0)
	for guarding := true; guarding; {
		select {
		case <-ctx.Done():
			return powerResult{stopped: true, log: fmt.Sprintf("the adapter on %s sent Image View On and Active Source for %s %s, and the wake stopped after %s", n.machine, physical, times(sends), elapsed(time.Since(began)))}
		case <-guard.C:
			guarding = false
			continue
		case <-n.sources:
		}
		holder := n.currentSource()
		if holder == physical {
			continue
		}
		if machine, other := job.others[holder]; other {
			message := fmt.Sprintf("the adapter on %s sent Image View On and Active Source for %s, Display %s, %s, and the adapter on %s sent Active Source for %s after %s, so the guard ended",
				n.machine, physical, display, times(sends), machine, holder, elapsed(time.Since(began)))
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
			return powerResult{stopped: true, log: fmt.Sprintf("the adapter on %s sent Image View On and Active Source for %s %s, and the wake stopped after %s", n.machine, physical, times(sends), elapsed(time.Since(began)))}
		case <-time.After(cecWakeSettle):
		}
		if n.currentSource() == physical {
			continue
		}
		if ended := send(); ended != nil {
			return *ended
		}
	}
	return sourceVerdict(n.machine, physical, display, n.currentSource(), sends, taken, takenBy, takenAfter)
}

// sourceVerdict states what the bus reports at the end of a guard.
func sourceVerdict(machine string, physical cec.PhysicalAddress, display string, holder cec.PhysicalAddress, sends, taken int, takenBy cec.PhysicalAddress, takenAfter time.Duration) powerResult {
	message := fmt.Sprintf("the adapter on %s sent Image View On and Active Source for %s, Display %s, %s", machine, physical, display, times(sends))
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
