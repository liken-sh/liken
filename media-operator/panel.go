package main

// The panel desk is the boundary between the bus and the
// reconcile loop for one unit's panel, the way the codes desk is
// for a controller. The idle screen client holds no API credentials, so
// it publishes its desire for the panel, and the pass turns the desk's
// newest desire into an override on the screen's Display.

import (
	"sync"
	"time"
)

// The two desires an idle screen client states. They are the values on
// the panel topic, not the states the Player status carries.
const (
	panelDesireOn  = "on"
	panelDesireOff = "off"
)

// The retained panel topic carries a desire and not a report.
// The unit it belongs to is named by the topic, not by the body.
type panelDesire struct {
	Desire string `json:"desire"`
}

// panelFromDisplay is the Player status word for what the
// display-operator last observed on the panel. The power word is one
// of on, standby, suspend, off, and hardOff, in either spelling, and
// every one but on is a panel held down, so all four read Off. A
// Display that observed nothing yet folds to no panel field at all.
func panelFromDisplay(observed DisplayObserved) string {
	switch {
	case observed.Power != "" && !samePower(observed.Power, displayPowerOn):
		return panelOff
	case observed.Brightness != nil && *observed.Brightness == 0:
		return panelBacklightOff
	case observed.Power == "" && observed.Brightness == nil:
		return ""
	}
	return panelOn
}

// panelDesk holds the newest state per unit under one mutex, because
// the bus handler runs on the bus reader's goroutine and the loop
// runs on its own. It also holds when each desire arrived, so a pass
// can tell a desire the client stated before the Player last turned on
// from one it stated since (desireFor).
type panelDesk struct {
	mutex   sync.Mutex
	state   map[string]string
	arrived map[string]time.Time
	wake    chan<- struct{}
	// now is the clock the arrival times read. It is a field so a test
	// orders a desire against a turn-on with no wait.
	now func() time.Time
}

func newPanelDesk(wake chan<- struct{}) *panelDesk {
	return &panelDesk{state: map[string]string{}, arrived: map[string]time.Time{}, wake: wake, now: time.Now}
}

// playerKey is the one key shape for a unit, the same namespace and
// name the topic carries.
func playerKey(namespace, name string) string {
	return namespace + "/" + name
}

// setState folds one desire. A desire that changed is a write
// the next pass owes the screen's Display, so it wakes the loop at
// once. A repeat of the desire already held wakes nothing and keeps
// the time the desire first arrived, because the retained topic
// delivers the same message again on every reconnect.
func (p *panelDesk) setState(key, state string) {
	p.mutex.Lock()
	previous, had := p.state[key]
	p.state[key] = state
	moved := !had || previous != state
	if moved {
		p.arrived[key] = p.now()
	}
	p.mutex.Unlock()
	if moved {
		poke(p.wake)
	}
}

// desireFor is one unit's panel desire as a pass acts on it. An off
// desire that arrived no later than the Player's last turn-on is the
// word the client stated when the Player turned off, and the retained
// topic holds it until the client answers the wake with on. A pass
// that acted on it would darken the panel it just lit, so the desire
// reads as no statement until the client states the next one. The
// turn-on time is the Player's status.poweredOnAt, at millisecond
// precision, so the arrival is compared at the same precision.
func (p *panelDesk) desireFor(key, poweredOnAt string) string {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	desire := p.state[key]
	if desire != panelDesireOff || poweredOnAt == "" {
		return desire
	}
	turnedOn, err := time.Parse(askTimeLayout, poweredOnAt)
	if err != nil {
		return desire
	}
	if !p.arrived[key].Truncate(time.Millisecond).After(turnedOn) {
		return ""
	}
	return desire
}

// stateFor is one unit's panel desire, empty for a unit no
// sidecar has stated one for, and the pass then writes no override
// and reads no Display.
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
			delete(p.arrived, key)
		}
	}
}
