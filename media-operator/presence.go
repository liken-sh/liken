package main

// The presence desk is the boundary between the bus and the reconcile
// loop for a controller's presence, the way the report desk is for a
// Play's status. Each standing remote pod publishes its controller's
// connected flag and its own availability, the operator subscribes to
// both, and the bus handler folds each message in here. The reconcile
// pass reads the desk when it builds a Player's bus status, so the idle
// screen reads one topic and no more.

import "sync"

// presenceDesk holds the newest presence per controller and the
// availability of the pod that reported it. One mutex covers both maps,
// because the bus handler runs on the bus reader's goroutine and the loop
// runs on its own.
//
// Both maps are keyed by controllerKey, the same namespace-and-name shape
// the focus desk uses, so the bus handler and the loop name a controller
// the same way.
type presenceDesk struct {
	mutex     sync.Mutex
	connected map[string]bool
	online    map[string]bool
	wake      chan<- struct{}
}

func newPresenceDesk(wake chan<- struct{}) *presenceDesk {
	return &presenceDesk{
		connected: map[string]bool{},
		online:    map[string]bool{},
		wake:      wake,
	}
}

// setConnected records one controller's presence. A change is what a
// person sees on the idle screen as a line that dims or pulses back, so it
// wakes the loop at once and the next pass republishes the Player status.
// A repeat of the value already held wakes nothing, because the retained
// topic delivers the same message again on every reconnect.
func (p *presenceDesk) setConnected(key string, connected bool) {
	p.mutex.Lock()
	previous, had := p.connected[key]
	p.connected[key] = connected
	p.mutex.Unlock()
	if !had || previous != connected {
		poke(p.wake)
	}
}

// setAvailability records whether the standing remote pod that reports one
// controller is up. A pod that goes offline folds its controller to
// disconnected in presenceFor, so the wake lets the pass rewrite the status
// that fold changes.
func (p *presenceDesk) setAvailability(key string, online bool) {
	p.mutex.Lock()
	previous, had := p.online[key]
	p.online[key] = online
	p.mutex.Unlock()
	if !had || previous != online {
		poke(p.wake)
	}
}

// presenceFor reports one controller's presence and whether the desk holds
// any. A controller the desk has never heard from is not held, and the
// status it appears in carries no connected key at all, because an
// unreported controller is neither connected nor disconnected. A
// controller whose pod is offline folds to disconnected, because a pod
// that is gone cannot claim its controller is there.
func (p *presenceDesk) presenceFor(key string) (connected, held bool) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	connected, held = p.connected[key]
	if !held {
		return false, false
	}
	if online, heard := p.online[key]; heard && !online {
		return false, true
	}
	return connected, true
}

// retain drops the controllers the cluster no longer holds. The pass hands
// over the set of Remotes that still exist, and the maps shrink to match,
// so a long-running operator does not accumulate a key per deleted Remote.
// The retained topics on the broker are the deleted pod's own to leave
// behind, and this desk clears none of them.
func (p *presenceDesk) retain(live map[string]bool) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for key := range p.connected {
		if !live[key] {
			delete(p.connected, key)
		}
	}
	for key := range p.online {
		if !live[key] {
			delete(p.online, key)
		}
	}
}
