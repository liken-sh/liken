package main

// The Activation and Deactivation steps run the procedures of the two
// lifecycle triggers. The tree orders them: activation runs the
// Observatory and then its devices, then the Telescope and then its
// devices, then the devices of each OpticalTrain, and deactivation
// runs the same tiers in reverse. The resources of one tier run in
// parallel, so a dome unparks before the mount does, and the mount
// parks before the dome does, with no order declared. Each step waits
// until every run it started has ended, so the next lifecycle step
// never begins under a procedure that still runs.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// stepRun is one resource's run in a step, with the transition time it
// answers.
type stepRun struct {
	res   resource
	since time.Time
}

// stepPlan is the tiers of one step, in the order they run.
type stepPlan struct {
	trigger string
	tiers   [][]stepRun
}

// runners answers the runs of the plan that have actions.
func (p *stepPlan) runners() []stepRun {
	var out []stepRun
	for _, tier := range p.tiers {
		for _, m := range tier {
			if len(m.res.actionsOf(p.trigger)) > 0 {
				out = append(out, m)
			}
		}
	}
	return out
}

// event answers the step as the event that each action's after waits
// on: the runs of the same step.
func (p *stepPlan) event() event {
	return event{member: func(_ *tree, key string) (membership, bool) {
		for _, m := range p.runners() {
			if m.res.key() == key {
				return membership{trigger: p.trigger, since: m.since}, true
			}
		}
		return membership{}, false
	}}
}

// tier answers the resources of a list of devices with one transition
// time.
func tier(t *tree, devices []*device, since time.Time) []stepRun {
	var out []stepRun
	for _, d := range devices {
		out = append(out, stepRun{t.ofDevice(d), since})
	}
	return out
}

// siteTiers are the Observatory and its devices.
func siteTiers(t *tree, site *observatory.Observatory, since time.Time) [][]stepRun {
	return [][]stepRun{
		{{ofObservatory(site), since}},
		tier(t, t.devicesOn(serverRef{observatory.ObservatoryKind, site.Metadata.Name}), since),
	}
}

// telescopeTiers are the Telescope, its own devices, and the devices
// of its trains.
func telescopeTiers(t *tree, telescope *observatory.Telescope, since time.Time) [][]stepRun {
	name := telescope.Metadata.Name
	var trains []*device
	for _, train := range sortedNames(t.trains) {
		if t.trains[train].Spec.Telescope == name {
			trains = append(trains, t.trainDevices(train)...)
		}
	}
	return [][]stepRun{
		{{ofTelescope(telescope), since}},
		tier(t, directDevices(t, observatory.TelescopeKind, name), since),
		tier(t, trains, since),
	}
}

// runTiers runs tiers from..to of a plan in order, the runs of each
// tier in parallel, and copies their actions into the step's record as
// they change. It answers the first failure of a tier, after every run
// of the tier has ended.
func (r *runner) runTiers(ctx context.Context, w *stepWork, plan *stepPlan, from, to int, ending bool) error {
	ev := plan.event()
	for _, members := range plan.tiers[from:to] {
		errs := make([]error, len(members))
		var group sync.WaitGroup
		for i, m := range members {
			group.Go(func() {
				errs[i] = r.o.runProcedure(ctx, procCall{
					res: m.res, trigger: plan.trigger, actions: m.res.actionsOf(plan.trigger),
					since: m.since, rerun: true, ending: ending, event: ev,
				})
			})
		}
		r.follow(w, plan, &group)
		for _, err := range errs {
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// follow copies the actions of a plan's runs into the step's record
// after each change, until the group of runs ends.
func (r *runner) follow(w *stepWork, plan *stepPlan, group *sync.WaitGroup) {
	finished := make(chan struct{})
	go func() {
		group.Wait()
		close(finished)
	}()
	for {
		wake := r.o.changed.wait()
		r.copyActions(w, plan)
		select {
		case <-finished:
			r.copyActions(w, plan)
			return
		case <-wake:
		}
	}
}

// copyActions copies the actions of a plan's runs into the step's
// record, each with its resource, and sets the step's summary to the
// actions that run now. The record is written when the summary
// changes, so each write changes what `kubectl get rsv -w` prints, and
// the step's end writes the last copy.
func (r *runner) copyActions(w *stepWork, plan *stepPlan) {
	var actions []observatory.StepAction
	var running []string
	for _, m := range plan.runners() {
		run, ok := r.o.runs.get(m.res.key(), plan.trigger)
		if !ok || !answers(run, m.since, time.Time{}) {
			continue
		}
		for _, a := range run.Actions {
			actions = append(actions, observatory.StepAction{Kind: m.res.kind.Name, Name: m.res.name(), ActionRun: a})
			if a.State == observatory.StepRunning {
				running = append(running, m.res.String()+": "+lowerFirst(firstNonEmpty(a.Summary, a.Action)))
			}
		}
	}
	w.r.step(w.name).Actions = actions
	if len(running) > 0 {
		w.report(strings.Join(running, "; "))
	}
}

// ran says what a plan's runs did, for the step's summary.
func ran(plan *stepPlan, notes []string) (outcome, error) {
	var names []string
	for _, m := range plan.runners() {
		names = append(names, m.res.String())
	}
	if len(names) == 0 {
		return skipped("%s", strings.Join(append([]string{"no " + plan.trigger + " procedures"}, notes...), "; "))
	}
	return done("%s", strings.Join(append([]string{"ran the " + plan.trigger + " of " + strings.Join(names, ", ")}, notes...), "; "))
}

// activation runs the activation procedures. Under the observatory's
// lock, it makes the Observatory Active, unless another reservation
// did, and runs the observatory's tiers. A second reservation in the
// observatory waits for the lock, and then finds those runs Done. It
// then makes the Telescope Active from the step's start, and runs the
// telescope's tiers.
func (r *runner) activation(ctx context.Context, w *stepWork) (outcome, error) {
	t := r.o.snapshot()
	telescope, site, err := r.siteOf(t)
	if err != nil {
		return outcome{}, err
	}
	start := *r.step(observatory.StepActivation).StartTime
	lock := r.o.siteLock(site.Metadata.Name)
	if err := lock.acquire(ctx); err != nil {
		return outcome{}, err
	}
	siteSince := r.o.activity.set(observatoryKey(site.Metadata.Name), true, stamp(), r.name)
	// A telescope turns Active at the start of its holder's Activation
	// step. A retry starts the step again, and the telescope keeps the
	// time it turned Active, so a run that is Done stays Done.
	scopeKey := telescopeKey(telescope.Metadata.Name)
	scopeSince := start
	if held := r.o.activity.get(scopeKey); held.active {
		scopeSince = held.since
	}
	plan := &stepPlan{trigger: observatory.TriggerActivation, tiers: append(siteTiers(t, site, siteSince), telescopeTiers(t, telescope, scopeSince)...)}
	err = r.runTiers(ctx, w, plan, 0, 2, false)
	lock.release()
	if err != nil {
		return outcome{}, err
	}
	r.o.activity.set(scopeKey, true, scopeSince, r.name)
	if err := r.runTiers(ctx, w, plan, 2, len(plan.tiers), false); err != nil {
		return outcome{}, err
	}
	return ran(plan, r.coolerNotes(t, telescope))
}

// coolerNotes names each camera of the telescope whose driver has a
// cooler and whose activation does not cool it, so a missing procedure
// is not silent.
func (r *runner) coolerNotes(t *tree, telescope *observatory.Telescope) []string {
	ref := serverRef{observatory.TelescopeKind, telescope.Metadata.Name}
	var notes []string
	for _, h := range r.o.handlesOf(t, ref, t.devicesOf(ref, observatory.CameraKind)) {
		if coolSetpoint(h.d) != nil {
			continue
		}
		if _, ok := h.client().Property(h.name, "CCD_COOLER"); ok {
			notes = append(notes, fmt.Sprintf("%s has a cooler and no cool action in spec.activation", h))
		}
	}
	return notes
}

// coolSetpoint answers the setpoint of a camera's first cool action in
// its activation, or nil when it has none.
func coolSetpoint(d *device) *float64 {
	for _, a := range d.object.Spec.Activation {
		if a.Cool != nil {
			return &a.Cool.Celsius
		}
	}
	return nil
}

// deactivation runs the deactivation procedures, while every device is
// still connected. It ends the Telescope's activity from the step's
// start, and runs the telescope's tiers in reverse. Then, under the
// observatory's lock, when every other telescope in the observatory
// has ended its activity and run its tiers, it ends the Observatory's
// activity and runs the observatory's tiers in reverse. An action on a device that is not
// connected, after an activation that failed before it connected the
// device, is Skipped.
func (r *runner) deactivation(ctx context.Context, w *stepWork) (outcome, error) {
	t := r.o.snapshot()
	telescope, site, err := r.siteOf(t)
	if err != nil {
		return skipped("%v", err)
	}
	start := *r.step(observatory.StepDeactivation).StartTime
	plan := &stepPlan{trigger: observatory.TriggerDeactivation}
	var notes []string
	if activated := r.step(observatory.StepActivation); activated != nil && activated.StartTime != nil {
		since := r.o.activity.set(telescopeKey(telescope.Metadata.Name), false, start, r.name)
		plan.tiers = reversed(telescopeTiers(t, telescope, since))
	} else {
		notes = append(notes, "Telescope "+telescope.Metadata.Name+" was not activated")
	}
	if err := r.runTiers(ctx, w, plan, 0, len(plan.tiers), true); err != nil {
		return outcome{}, err
	}
	r.o.activity.settle(telescopeKey(telescope.Metadata.Name))
	lock := r.o.siteLock(site.Metadata.Name)
	if err := lock.acquire(ctx); err != nil {
		return outcome{}, err
	}
	defer lock.release()
	if other := r.otherTelescope(site.Metadata.Name); other != "" {
		return ran(plan, append(notes, "Observatory "+site.Metadata.Name+" stays active for Telescope "+other))
	}
	key := observatoryKey(site.Metadata.Name)
	// The observatory's tiers run when this step ends its activity, or
	// when this step ended it before an operator restart.
	if state := r.o.activity.get(key); !state.active && state.by != r.name {
		return ran(plan, notes)
	}
	since := r.o.activity.set(key, false, stamp(), r.name)
	first := len(plan.tiers)
	plan.tiers = append(plan.tiers, reversed(siteTiers(r.o.snapshot(), site, since))...)
	if err := r.runTiers(ctx, w, plan, first, len(plan.tiers), true); err != nil {
		return outcome{}, err
	}
	return ran(plan, notes)
}

// otherTelescope answers another telescope in the observatory that is
// Active, or that has not finished its deactivation tiers, or "". Two
// reservations that end together both run their Deactivation steps
// while both hold their telescopes, so a test of the holders would
// leave the observatory Active. The last telescope to settle ends the
// observatory's activity, after every mount ran its deactivation.
func (r *runner) otherTelescope(site string) string {
	t := r.o.snapshot()
	for _, name := range sortedNames(t.telescopes) {
		if name == r.res.Spec.Telescope || t.telescopes[name].Spec.Observatory != site {
			continue
		}
		if s := r.o.activity.get(telescopeKey(name)); s.active || (!s.since.IsZero() && !s.settled) {
			return name
		}
	}
	return ""
}

func reversed(tiers [][]stepRun) [][]stepRun {
	out := make([][]stepRun, 0, len(tiers))
	for i := len(tiers) - 1; i >= 0; i-- {
		out = append(out, tiers[i])
	}
	return out
}
