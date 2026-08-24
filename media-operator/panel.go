package main

// The panel desk is the boundary between the bus and the reconcile
// loop for one unit's panel, the way the presence desk is for a
// controller. The idle sidecar holds no API credentials, so it
// publishes what it actuated on the panel topic, and the pass folds
// the desk's newest state into the Player's status.

import "sync"

// panelDesk holds the newest state per unit under one mutex, because
// the bus handler runs on the bus reader's goroutine and the loop
// runs on its own.
type panelDesk struct {
	mutex sync.Mutex
	state map[string]string
	wake  chan<- struct{}
}

func newPanelDesk(wake chan<- struct{}) *panelDesk {
	return &panelDesk{state: map[string]string{}, wake: wake}
}

// playerKey is the one key shape for a unit, the same namespace and
// name the topic carries.
func playerKey(namespace, name string) string {
	return namespace + "/" + name
}

// setState folds one report. A panel that went dark or came back is
// a change a person reads in kubectl, so it wakes the loop at once. A
// repeat of the state already held wakes nothing, because the
// retained topic delivers the same message again on every reconnect.
func (p *panelDesk) setState(key, state string) {
	p.mutex.Lock()
	previous, had := p.state[key]
	p.state[key] = state
	p.mutex.Unlock()
	if !had || previous != state {
		poke(p.wake)
	}
}

// stateFor is one unit's panel state, empty for a unit no sidecar
// has reported, so a Player with no control device carries no panel
// field at all.
func (p *panelDesk) stateFor(key string) string {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.state[key]
}

// retain drops the units the cluster no longer holds, so a
// long-running operator does not accumulate a key per deleted
// Player.
func (p *panelDesk) retain(live map[string]bool) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for key := range p.state {
		if !live[key] {
			delete(p.state, key)
		}
	}
}
