package main

// The idle screen client has no API credentials, so it publishes the
// panel state it wants, its desire, on the bus. panelCache caches the
// newest desire of each Player, and the reconcile pass writes it as an
// override on the screen's Display.

import (
	"sync"
	"time"
)

// The desires that the idle screen client publishes on the panel
// topic. The panel states in the Player's status are different values.
const (
	panelDesireOn  = "on"
	panelDesireOff = "off"
)

// panelDesire is the body of a message on the retained panel topic.
// The topic names the Player, so the body does not.
type panelDesire struct {
	Desire string `json:"desire"`
}

// panelFromDisplay returns the Player's panel status from what
// display-operator last observed on the Display. Every power value
// except on, in any case, is Off. A backlight at zero on a panel that
// is not powered down is BacklightOff. A Display that has observed
// nothing returns an empty string, which leaves the status unset.
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

// panelCache caches the newest panel desire of each Player, and when it
// arrived. The bus reader's goroutine writes it and the reconcile loop
// reads it, so a mutex guards the maps.
type panelCache struct {
	mutex   sync.Mutex
	state   map[string]string
	arrived map[string]time.Time
	wake    chan<- struct{}
	// now returns the arrival time. A test replaces it.
	now func() time.Time
}

func newPanelCache(wake chan<- struct{}) *panelCache {
	return &panelCache{state: map[string]string{}, arrived: map[string]time.Time{}, wake: wake, now: time.Now}
}

// playerKey returns the namespace/name key of a Player.
func playerKey(namespace, name string) string {
	return namespace + "/" + name
}

// setState records a desire and starts a pass when it changed. The
// broker sends the retained message again on every reconnect, so a
// repeat of the cached desire starts no pass and keeps its first
// arrival time.
func (p *panelCache) setState(key, state string) {
	p.mutex.Lock()
	previous, had := p.state[key]
	p.state[key] = state
	moved := !had || previous != state
	if moved {
		p.arrived[key] = p.now()
	}
	p.mutex.Unlock()
	if moved {
		signalWake(p.wake)
	}
}

// desireFor returns the Player's panel desire for a pass to act on.
// When the Player turns on, the broker still retains the off desire
// that the client published when it turned off, until the client
// publishes on. Acting on that desire would turn the panel off again,
// so desireFor returns an empty string for an off desire that arrived
// no later than poweredOnAt. poweredOnAt is status.poweredOnAt, which
// has millisecond precision, so the arrival time is truncated to
// milliseconds before the compare.
func (p *panelCache) desireFor(key, poweredOnAt string) string {
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

// stateFor returns the Player's cached panel desire, or an empty
// string when the client has published none.
func (p *panelCache) stateFor(key string) string {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.state[key]
}

// retain deletes the desire of each Player that is not in live, so the
// cache keeps no entry for a deleted Player.
func (p *panelCache) retain(live map[string]bool) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for key := range p.state {
		if !live[key] {
			delete(p.state, key)
			delete(p.arrived, key)
		}
	}
}
