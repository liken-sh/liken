package main

// The Active condition of each Telescope and Observatory. Activation
// procedures run as it turns True, and deactivation procedures as it
// turns False, and each run records the transition time it answers.
// So the time must survive an operator restart: a new copy that read
// another time would run every procedure again.
//
// A Telescope is Active from the start of its holder's Activation step
// until the start of its Deactivation step, and the reservation's
// record gives both times. An Observatory is Active from the
// Activation step of the first reservation in it until the
// Deactivation step of the last one, and no single record holds that,
// so a new copy reads the time from the Observatory's stored Active
// condition.

import (
	"sync"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

type activeState struct {
	active bool
	// since is when the state began, or zero for a resource that was
	// never Active.
	since time.Time
	// by names the reservation whose step set the state.
	by string
	// settled is true when a resource that is not Active has run its
	// deactivation tiers, or never was Active. The Observatory's
	// deactivation waits for every Telescope in it to settle, so the
	// dome parks after every mount's deactivation.
	settled bool
}

type activity struct {
	mu      sync.Mutex
	states  map[string]activeState
	changed *bell
}

func newActivity(changed *bell) *activity {
	return &activity{states: map[string]activeState{}, changed: changed}
}

func telescopeKey(name string) string   { return observatory.TelescopeKind.Name + "/" + name }
func observatoryKey(name string) string { return observatory.ObservatoryKind.Name + "/" + name }

func (a *activity) get(key string) activeState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.states[key]
}

// set records a state, and answers the time the state began: since for
// a change, and the time already held when the state holds already.
func (a *activity) set(key string, active bool, since time.Time, by string) time.Time {
	a.mu.Lock()
	held := a.states[key]
	if held.active == active && !held.since.IsZero() {
		a.mu.Unlock()
		return held.since
	}
	a.states[key] = activeState{active: active, since: since, by: by}
	a.mu.Unlock()
	a.changed.notify()
	return since
}

// settle records that a resource that is not Active ran its
// deactivation.
func (a *activity) settle(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if held := a.states[key]; !held.active {
		held.settled = true
		a.states[key] = held
	}
}

// seed reads the states from the reservations' records and the stored
// conditions, after the claims are seeded.
func (a *activity) seed(t *tree, c *claims) {
	a.mu.Lock()
	defer a.mu.Unlock()
	stored := func(conditions []observatory.Condition) activeState {
		cond := conditionOf(conditions, observatory.ConditionActive)
		active := cond.Status == observatory.ConditionTrue
		return activeState{active: active, since: cond.LastTransitionTime, settled: !active}
	}
	for name, telescope := range t.telescopes {
		state := stored(telescope.Status.Conditions)
		if holder, ok := c.holderOf(name); ok && t.reservations[holder] != nil {
			// The holder's record decides. A holder that has not begun
			// its Activation step leaves the telescope not Active.
			from, ok := recordedActivity(t.reservations[holder])
			switch {
			case !ok && !state.active:
				from.since = state.since
			case from.active && state.active:
				// A retry of the Activation step moves its start, and
				// the telescope keeps the time it first turned Active.
				from.since = state.since
			}
			if !ok {
				from.settled = true
			}
			state = from
		}
		a.states[telescopeKey(name)] = state
	}
	for name, site := range t.observatories {
		state := stored(site.Status.Conditions)
		// The stored condition can be older than the last activation,
		// when the operator stopped before the status writer wrote it.
		// A telescope that is Active makes its observatory Active, from
		// the first such telescope's activation.
		if !state.active {
			for telescope, scope := range t.telescopes {
				held := a.states[telescopeKey(telescope)]
				if scope.Spec.Observatory == name && held.active && (!state.active || held.since.Before(state.since)) {
					state = activeState{active: true, since: held.since, by: held.by}
				}
			}
		}
		a.states[observatoryKey(name)] = state
	}
}

// recordedActivity answers a telescope's state from its holder's steps:
// Active from the start of the Activation step, and not Active from the
// start of the Deactivation step.
func recordedActivity(r *observatory.Reservation) (activeState, bool) {
	if s := findStep(r.Status.Steps, observatory.StepDeactivation); s != nil && s.StartTime != nil {
		return activeState{since: *s.StartTime, by: r.Metadata.Name, settled: ended(s.State)}, true
	}
	if s := findStep(r.Status.Steps, observatory.StepActivation); s != nil && s.StartTime != nil {
		return activeState{active: true, since: *s.StartTime, by: r.Metadata.Name}, true
	}
	return activeState{}, false
}

// condition answers the Active condition of a Telescope or an
// Observatory.
func (a *activity) condition(key string) observatory.Condition {
	state := a.get(key)
	c := condition(observatory.ConditionActive, observatory.ConditionFalse, "Inactive", "Not activated")
	switch {
	case state.active:
		c = condition(observatory.ConditionActive, observatory.ConditionTrue, "Active", "Activated by Reservation "+state.by)
	case !state.since.IsZero() && state.by != "":
		c.Message = "Deactivated by Reservation " + state.by
	case !state.since.IsZero():
		c.Message = "Deactivated"
	}
	if !state.since.IsZero() {
		c.LastTransitionTime = state.since
	}
	return c
}

// forgetUnheld ends the activity of each Telescope that no reservation
// holds, and of each Observatory with no held telescope. A
// reservation that went away with no deactivation, such as one whose
// finalizer a person removed, leaves its telescope Active, and this
// ends it with no procedure: the reservation's own deactivation would
// have run them.
func (a *activity) forgetUnheld(t *tree, c *claims) {
	held := c.held()
	sites := map[string]bool{}
	for telescope := range held {
		if scope, ok := t.telescopes[telescope]; ok {
			sites[scope.Spec.Observatory] = true
		}
	}
	for name := range t.telescopes {
		if !held[name] && a.get(telescopeKey(name)).active {
			a.set(telescopeKey(name), false, stamp(), "")
			a.settle(telescopeKey(name))
		}
	}
	for name := range t.observatories {
		if !sites[name] && a.get(observatoryKey(name)).active {
			a.set(observatoryKey(name), false, stamp(), "")
		}
	}
}
