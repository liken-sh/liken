package main

// The Activation and Deactivation steps run the procedures of a
// reservation's lifecycle triggers. Activation runs these tiers in turn: the Observatory, its devices,
// the Telescope, its devices without an optical train, then its devices
// that name one. Deactivation runs the same tiers in reverse. The
// resources of one tier run in parallel and declare no order among
// themselves, so a dome can unpark before the mount does, and the mount
// can park before the dome does. A step waits for every run it started
// to end, so no procedure is still running when the next lifecycle step
// begins.
//
// Activation runs the devices that the tree places on each server.
// Deactivation runs the devices whose driver pod runs on each server,
// even when their parents no longer place them there. A person can
// delete a device or its train together with the reservation, and a
// driver that still runs can still close a dust cap or warm a camera.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// stepRun is one resource's run in a step. since is the transition time
// of the run. on is the server that a device left, for a run that acts
// through the driver that still runs there (procCall.on), and zero for
// every other run.
type stepRun struct {
	res   resource
	since time.Time
	on    serverRef
}

// stepPlan is the tiers of one step, in the order they run.
type stepPlan struct {
	trigger string
	tiers   [][]stepRun
}

// runners returns the runs of the plan whose resource has actions for the
// plan's trigger.
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

// event returns the event that an action's after field waits on in this
// step. Its members are the resources that have a run in the step.
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

// tier returns the runs of devices on one server, all with one
// transition time. When a device's parents no longer place it on the
// server but its driver still runs there, the run uses the resource that
// leaver builds (leaves.go), so the deactivation resolves references on
// the server that the device left.
func tier(t *tree, ref serverRef, devices []*device, since time.Time) []stepRun {
	var out []stepRun
	for _, d := range devices {
		run := stepRun{res: t.ofDevice(d), since: since}
		if on, placed := t.server(d); !placed || on != ref {
			run.res, run.on = leaver(t, d, ref), ref
		}
		out = append(out, run)
	}
	return out
}

// siteTiers returns the Observatory as the first tier, then the devices
// of its server. Devices that run a driver come before the devices they
// serve (deviceTiers).
func siteTiers(t *tree, site *observatory.Observatory, devices []*device, since time.Time) [][]stepRun {
	ref := serverRef{observatory.ObservatoryKind, site.Metadata.Name}
	tiers := [][]stepRun{{{res: ofObservatory(site), since: since}}}
	for _, devices := range deviceTiers(t, [][]*device{devices}) {
		tiers = append(tiers, tier(t, ref, devices, since))
	}
	return tiers
}

// telescopeTiers returns the Telescope as the first tier, then the
// devices of its server that name no optical train, then the devices
// that name one. Within each group, devices that run a driver come
// before the devices they serve (deviceTiers). The device's own
// spec.opticalTrain decides the group, not the OpticalTrain resource,
// because a device whose train a person deleted still runs on the server
// during Deactivation, and it must still deactivate before the devices
// that name no train.
func telescopeTiers(t *tree, telescope *observatory.Telescope, devices []*device, since time.Time) [][]stepRun {
	ref := serverRef{observatory.TelescopeKind, telescope.Metadata.Name}
	var own, trains []*device
	for _, d := range devices {
		if d.object.Spec.OpticalTrain != "" {
			trains = append(trains, d)
		} else {
			own = append(own, d)
		}
	}
	tiers := [][]stepRun{{{res: ofTelescope(telescope), since: since}}}
	for _, devices := range deviceTiers(t, [][]*device{own, trains}) {
		tiers = append(tiers, tier(t, ref, devices, since))
	}
	return tiers
}

// withoutLacking removes the devices whose INDI device does not
// implement their kind's interface (lackingOf). If the driver defines
// the interface later, their activation runs then (lateactivation.go).
func (o *operator) withoutLacking(tiers [][]stepRun) [][]stepRun {
	out := make([][]stepRun, 0, len(tiers))
	for _, members := range tiers {
		out = append(out, slices.DeleteFunc(slices.Clone(members), func(m stepRun) bool {
			return m.res.device != nil && o.lackingOf(m.res.device) != ""
		}))
	}
	return out
}

// runTiers runs plan.tiers[from:to] in order. The runs of one tier run
// in parallel, and their actions are copied into
// the step's record as they change. When every run of a tier has ended,
// runTiers returns the first error of the tier and starts no later tier.
func (r *runner) runTiers(ctx context.Context, w *stepWork, plan *stepPlan, from, to int, ending bool) error {
	ev := plan.event()
	for _, members := range plan.tiers[from:to] {
		errs := make([]error, len(members))
		var group sync.WaitGroup
		for i, m := range members {
			group.Go(func() {
				errs[i] = r.o.runProcedure(ctx, procCall{
					res: m.res, trigger: plan.trigger, actions: m.res.actionsOf(plan.trigger),
					since: m.since, rerun: true, ending: ending, on: m.on, event: ev,
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

// follow copies the actions of the plan's runs into the step's record
// each time one changes, until every run in group has ended.
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

// copyActions copies the actions of the plan's runs, each with its
// resource, into the step's record, and sets the step's summary to the
// actions that are running. A change to the summary is written to the
// Reservation, so `kubectl get rsv -w` prints it. The end of the step
// writes the final copy.
func (r *runner) copyActions(w *stepWork, plan *stepPlan) {
	var actions []observatory.StepAction
	var running []string
	for _, m := range plan.runners() {
		run, ok := r.o.runs.get(m.res.record(), plan.trigger)
		if !ok || !isRunFor(run, m.since, time.Time{}) {
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

// ran returns the step's outcome: skipped when no run has actions, and
// done otherwise, with notes added to the summary.
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

// activation runs the activation procedures. It takes the observatory's
// lock, makes the Observatory Active unless another reservation already
// did, runs the observatory's tiers, and releases the lock. A second
// reservation on the observatory waits for the lock, and then finds
// those runs Done. Then it makes the Telescope Active from the start of
// the step, and runs the telescope's tiers.
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
	// A telescope becomes Active at the start of its holder's Activation
	// step. A retry of the step keeps the original time, so a run that is
	// Done stays Done.
	scopeKey := telescopeKey(telescope.Metadata.Name)
	scopeSince := start
	if held := r.o.activity.get(scopeKey); held.active {
		scopeSince = held.since
	}
	siteRuns := siteTiers(t, site, t.devicesOn(serverRef{observatory.ObservatoryKind, site.Metadata.Name}), siteSince)
	scopeRuns := telescopeTiers(t, telescope, t.devicesOn(serverRef{observatory.TelescopeKind, telescope.Metadata.Name}), scopeSince)
	plan := &stepPlan{trigger: observatory.TriggerActivation, tiers: r.o.withoutLacking(append(siteRuns, scopeRuns...))}
	err = r.runTiers(ctx, w, plan, 0, len(siteRuns), false)
	lock.release()
	if err != nil {
		return outcome{}, err
	}
	r.o.activity.set(scopeKey, true, scopeSince, r.name)
	if err := r.runTiers(ctx, w, plan, len(siteRuns), len(plan.tiers), false); err != nil {
		return outcome{}, err
	}
	return ran(plan, r.coolerNotes(t, telescope))
}

// coolerNotes returns a note for each camera of the telescope whose
// driver has a cooler (CCD_COOLER) and whose spec.activation has no cool
// action, so the missing procedure shows in the step's summary.
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

// coolSetpoint returns the setpoint of the first cool action in a
// camera's spec.activation, or nil when it has none.
func coolSetpoint(d *device) *float64 {
	for _, a := range d.object.Spec.Activation {
		if a.Cool != nil {
			return &a.Cool.Celsius
		}
	}
	return nil
}

// deactivation runs the deactivation procedures while every device is
// still connected. It ends the Telescope's activity from the start of
// the step, and runs the telescope's tiers in reverse. Then it takes the
// observatory's lock. When every other telescope in the observatory has
// ended its activity and run its tiers, it ends the Observatory's
// activity and runs the observatory's tiers in reverse. An action on a
// device that is not connected is Skipped, as happens when an
// activation failed before it connected the device.
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
		ref := serverRef{observatory.TelescopeKind, telescope.Metadata.Name}
		plan.tiers = reversed(telescopeTiers(t, telescope, t.devicesRunningOn(ref), since))
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
	// Run the observatory's tiers when the Observatory is still Active,
	// or when this reservation already ended it before an operator
	// restart. Skip them when another reservation ended it.
	if state := r.o.activity.get(key); !state.active && state.by != r.name {
		return ran(plan, notes)
	}
	since := r.o.activity.set(key, false, stamp(), r.name)
	first := len(plan.tiers)
	now := r.o.snapshot()
	siteRef := serverRef{observatory.ObservatoryKind, site.Metadata.Name}
	plan.tiers = append(plan.tiers, reversed(siteTiers(now, site, now.devicesRunningOn(siteRef), since))...)
	if err := r.runTiers(ctx, w, plan, first, len(plan.tiers), true); err != nil {
		return outcome{}, err
	}
	return ran(plan, notes)
}

// otherTelescope returns the name of another telescope in the
// observatory that is Active or has not finished its deactivation
// tiers, or "" when there is none. It reads the telescopes' activity,
// not their holders, because two reservations that end together both
// still hold their telescopes during Deactivation. Each would find the
// other, and neither would end the observatory's activity.
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

// reversed returns tiers in the opposite order.
func reversed(tiers [][]stepRun) [][]stepRun {
	out := make([][]stepRun, 0, len(tiers))
	for i := len(tiers) - 1; i >= 0; i-- {
		out = append(out, tiers[i])
	}
	return out
}
