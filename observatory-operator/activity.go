package main

// The Active condition of each Telescope and Observatory. The operator
// runs activation procedures when Active turns True and deactivation
// procedures when it turns False, and each run records the transition
// time it ran for. A restarted operator must compute the same time, or
// it runs every procedure again.
//
// A Telescope is Active from the start of its holder's Activation step
// until the start of its Deactivation step, and the Reservation's
// status.steps records both times. An Observatory is Active from the
// Activation step of its first Reservation until the Deactivation step
// of its last. No single Reservation records that, so a restarted
// operator reads the Observatory's time from its stored Active
// condition.

import (
	"sync"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

type activeState struct {
	active bool
	// since is when the state began. It is zero for a resource that
	// has never been Active.
	since time.Time
	// by is the name of the Reservation whose step set the state.
	by string
	// settled is true when a resource that is not Active has finished
	// its deactivation tiers, or was never Active. An Observatory's
	// deactivation waits until every Telescope in it is settled, so
	// the dome parks after every mount.
	settled bool
}

type activity struct {
	mu      sync.Mutex
	states  map[string]activeState
	changed *changeNotifier
}

func newActivity(changed *changeNotifier) *activity {
	return &activity{states: map[string]activeState{}, changed: changed}
}

func telescopeKey(name string) string   { return observatory.TelescopeKind.Name + "/" + name }
func observatoryKey(name string) string { return observatory.ObservatoryKind.Name + "/" + name }

func (a *activity) get(key string) activeState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.states[key]
}

// set records a state and returns the time it began: since for a
// change, or the recorded time when the key already has that state. A
// change clears settled.
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

// settle records that a resource that is not Active has finished its
// deactivation.
func (a *activity) settle(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if held := a.states[key]; !held.active {
		held.settled = true
		a.states[key] = held
	}
}

// load rebuilds the states after an operator restart, from the
// Reservations' steps and the stored Active conditions. It runs after
// claims.load, because it reads each telescope's holder.
func (a *activity) load(t *tree, c *claims) {
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
			// The holder's steps override the stored condition. Before
			// the holder starts its Activation step, the telescope is
			// not Active.
			from, ok := recordedActivity(t.reservations[holder])
			switch {
			case !ok && !state.active:
				from.since = state.since
			case from.active && state.active:
				// A retry of the Activation step moves its start time, and
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
		// An Active telescope makes its Observatory Active, from the
		// time of the earliest such telescope.
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

// recordedActivity returns a telescope's state from its holder's steps.
// The telescope is Active from the start of the Activation step and not
// Active from the start of the Deactivation step. The bool is false
// when neither step has started.
func recordedActivity(r *observatory.Reservation) (activeState, bool) {
	if s := findStep(r.Status.Steps, observatory.StepDeactivation); s != nil && s.StartTime != nil {
		return activeState{since: *s.StartTime, by: r.Metadata.Name, settled: ended(s.State)}, true
	}
	if s := findStep(r.Status.Steps, observatory.StepActivation); s != nil && s.StartTime != nil {
		return activeState{active: true, since: *s.StartTime, by: r.Metadata.Name}, true
	}
	return activeState{}, false
}

// condition returns the Active condition of a Telescope or an
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

// forgetUnheld sets each Telescope that no Reservation holds to not
// Active, and each Observatory with no held Telescope too. A
// Reservation that disappears without deactivating, such as one whose
// finalizer a person removed, leaves its telescope Active. forgetUnheld
// clears that state without running deactivation procedures, because
// only a Reservation's Deactivation step runs them.
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
