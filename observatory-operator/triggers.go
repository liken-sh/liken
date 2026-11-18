package main

// A trigger is an item of a resource's spec.triggers. It runs its
// actions once each time its condition changes to the status that the
// trigger names. The trigger controller evaluates every trigger of
// every resource again each time a store, a run's record, or a
// resource's activity changes.
//
// The controller detects a transition by the condition's
// lastTransitionTime, which changes only when the status changes. A run
// records that time in its Since, so a trigger does not run twice for
// one transition.
//
// A trigger runs only while its resource is active. A resource becomes
// active when its activation run ends, or, if it has no activation,
// when its Telescope or Observatory becomes active. It stops being
// active when its deactivation begins. So when a condition already has
// the trigger's status at the end of the activation, the trigger runs
// then.
//
// A run that has started continues to its end, whatever its condition
// does in the meantime, because a weather station that flaps, or that
// reports Safe as Unknown while it reconnects, must not stop a park
// halfway. Only the resource's deactivation, the deletion of its
// object, or the operator's shutdown cancels a run.
//
// A resource runs one procedure at a time (procedure.go). A run that is
// due while another run of the resource is in progress waits for it,
// and then starts only if its condition still has the same status and
// transition time. Otherwise its record gives the reason it did not
// start.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func triggerName(i int) string { return fmt.Sprintf("triggers[%d]", i) }

// triggerRun is a trigger's run that is in progress.
type triggerRun struct {
	// record is the record key of the resource that the run belongs to.
	record string
	cancel context.CancelCauseFunc
	done   chan struct{}
}

// control is the state that the trigger controller keeps between
// evaluations.
type control struct {
	// inProgress holds the runs in progress, keyed by the resource's
	// record key and the trigger name.
	inProgress map[string]*triggerRun
	// retried holds the resourceVersion of each resource whose retry
	// annotation the controller handled. The controller removes the
	// annotation, and without this map an evaluation that runs before
	// the store shows the removal would retry a second time.
	retried map[string]string
	group   sync.WaitGroup
}

// keepTriggers evaluates the triggers each time structure is notified and
// each time a for period ends, until ctx is done.
func (o *operator) keepTriggers(ctx context.Context) {
	k := &control{inProgress: map[string]*triggerRun{}, retried: map[string]string{}}
	defer k.group.Wait()
	for {
		wake := o.structure.wait()
		var due time.Time
		if o.stores.ready() && o.loaded.Load() {
			due = o.evaluateTriggers(ctx, o.snapshot(), k)
		}
		// A clock: the timer fires when the earliest for period ends.
		var fire <-chan time.Time
		var timer *time.Timer
		if !due.IsZero() {
			timer = time.NewTimer(time.Until(due))
			fire = timer.C
		}
		select {
		case <-ctx.Done():
		case <-wake:
		case <-fire:
		}
		if timer != nil {
			timer.Stop()
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// evaluateTriggers starts the run of each trigger that is due and
// cancels the runs of resources that are no longer active. It returns
// the earliest time at which a trigger's for period ends, or the zero
// time if none is waiting. For a resource with the retry annotation
// (retry.go), it also starts each failed run again whose condition
// still has the same status and transition time.
func (o *operator) evaluateTriggers(ctx context.Context, t *tree, k *control) time.Time {
	inProgress := k.inProgress
	for id, f := range inProgress {
		select {
		case <-f.done:
			delete(inProgress, id)
		default:
		}
	}
	var due time.Time
	live := map[string]bool{}
	times := newPassTimes()
	defer o.series.m.observePasses(times)
	defer times.stop()
	for _, r := range t.withProcedures() {
		times.start(r.kind)
		live[r.record()] = true
		retry := k.hasRetryAnnotation(r)
		o.lateActivation(ctx, t, r, k, retry)
		period, active := o.activePeriod(r)
		for i, trigger := range r.procedures.Triggers {
			name := triggerName(i)
			id := r.record() + "/" + name
			f := inProgress[id]
			if !active {
				if f != nil {
					f.cancel(fmt.Errorf("the activity of %s ended", r))
				}
				continue
			}
			if f != nil {
				continue
			}
			when := trigger.When
			targets, err := t.resolve(r, name+".when", when.Kind, when.Name, false)
			if err != nil {
				// An unresolved trigger is an error in the spec. The
				// resource's status reports it, and the other triggers
				// are still evaluated.
				o.refuseTrigger(r, name, err)
				continue
			}
			wait, err := time.ParseDuration(firstNonEmpty(when.For, "0s"))
			if err != nil || wait < 0 {
				o.refuseTrigger(r, name, fmt.Errorf("%s.when.for: %q is not a duration", name, when.For))
				continue
			}
			conditions, _ := t.conditionsOf(targets[0].kind, targets[0].name)
			want := conditionStatus(when.Status)
			c := conditionOf(conditions, when.Type)
			holds, since := c.Status == want, c.LastTransitionTime
			call := procCall{res: r, trigger: name, condition: conditionText(targets[0], when), actions: trigger.Run, period: period}
			if run, ok := o.runs.get(r.record(), name); ok && run.State == observatory.StepRunning && run.Since != nil && isRunFor(run, *run.Since, period) {
				// The run was still Running when the operator last
				// stopped. Resume it even if its condition has changed.
				call.since, call.event = *run.Since, o.conditionEvent(r, targets[0], when.Type, want, *run.Since)
				inProgress[id] = o.startRun(ctx, call, &k.group)
				continue
			}
			if !holds {
				continue
			}
			if run, ok := o.runs.get(r.record(), name); ok && isRunFor(run, since, period) {
				if !retry || run.State != observatory.StepFailed {
					continue
				}
				call.rerun = true
			}
			if at := since.Add(wait); time.Now().Before(at) {
				if due.IsZero() || at.Before(due) {
					due = at
				}
				continue
			}
			call.since, call.event = since, o.conditionEvent(r, targets[0], when.Type, want, since)
			inProgress[id] = o.startRun(ctx, call, &k.group)
		}
		if retry {
			o.removeRetryAnnotation(ctx, r, k)
		}
	}
	// Cancel the runs of deleted objects. An object created again with
	// the same name has its own record key, so its runs can start
	// before the old object's runs have ended.
	for _, f := range inProgress {
		if !live[f.record] {
			f.cancel(errors.New("the resource was deleted"))
		}
	}
	return due
}

// conditionText describes a trigger's condition for a person, such as
// "WeatherStation lab Safe=False" or "WeatherStation lab Safe=True for
// 20m".
func conditionText(g target, when observatory.When) string {
	text := fmt.Sprintf("%s %s=%s", g, when.Type, conditionStatus(when.Status))
	if when.For != "" {
		text += " for " + when.For
	}
	return text
}

// startRun runs one trigger's procedure in a new goroutine of group and
// returns its triggerRun. When the run ends, the goroutine closes the
// triggerRun's done channel and then notifies structure, so the controller
// evaluates again.
func (o *operator) startRun(ctx context.Context, call procCall, group *sync.WaitGroup) *triggerRun {
	runCtx, cancel := context.WithCancelCause(ctx)
	f := &triggerRun{record: call.res.record(), cancel: cancel, done: make(chan struct{})}
	group.Go(func() {
		defer o.structure.notify()
		defer close(f.done)
		defer cancel(nil)
		if err := o.runProcedure(runCtx, call); err != nil && runCtx.Err() == nil {
			o.logf("%s %s: %v", call.res, call.trigger, err)
		}
	})
	return f
}

// withProcedures returns each Observatory, each Telescope, and each
// device, the kinds that can have procedures.
func (t *tree) withProcedures() []resource {
	var out []resource
	for _, name := range sortedNames(t.observatories) {
		out = append(out, ofObservatory(t.observatories[name]))
	}
	for _, name := range sortedNames(t.telescopes) {
		out = append(out, ofTelescope(t.telescopes[name]))
	}
	for _, d := range t.devices {
		out = append(out, t.ofDevice(d))
	}
	return out
}

// activePeriod returns when the resource's current activity began, and
// false when the resource is not active. A resource is not active
// while its Telescope or Observatory is not Active, and, if it has an
// activation, until the activation run for this activity is Done or
// Skipped.
func (o *operator) activePeriod(r resource) (time.Time, bool) {
	key, ok := activeParent(r)
	if !ok {
		return time.Time{}, false
	}
	state := o.activity.get(key)
	if !state.active {
		return time.Time{}, false
	}
	if len(r.procedures.Activation) > 0 {
		run, ok := o.runs.get(r.record(), observatory.TriggerActivation)
		if !ok || !isRunFor(run, state.since, time.Time{}) || !ended(run.State) {
			return time.Time{}, false
		}
	}
	return state.since, true
}

// conditionEvent returns the event for the transition of g's condition
// conditionType to status at since. An action's after waits on the
// event's members: the runs of every active resource that has a
// trigger on the same condition and status. The event is over when the
// condition's status or transition time changes, or when r stops
// being active. r stops being active before the controller cancels its
// runs one at a time, so a run that waited behind a canceled run does
// not start.
func (o *operator) conditionEvent(r resource, g target, conditionType string, status observatory.ConditionStatus, since time.Time) event {
	over := func(t *tree) error {
		if _, active := o.activePeriod(r); !active {
			return fmt.Errorf("the activity of %s ended", r)
		}
		conditions, _ := t.conditionsOf(g.kind, g.name)
		switch c := conditionOf(conditions, conditionType); {
		case c.Status != status:
			return fmt.Errorf("%s %s is no longer %s", g, conditionType, status)
		case !c.LastTransitionTime.Equal(since):
			return fmt.Errorf("%s %s changed to %s again at %s", g, conditionType, status, c.LastTransitionTime.UTC().Format(time.RFC3339))
		}
		return nil
	}
	return event{over: over, member: func(t *tree, key string) (membership, bool) {
		kindName, name, _ := strings.Cut(key, "/")
		kind, _ := observatory.KindNamed(kindName)
		x, ok := t.resource(kind, name)
		if !ok {
			return membership{}, false
		}
		period, active := o.activePeriod(x)
		if !active {
			return membership{}, false
		}
		for j, trigger := range x.procedures.Triggers {
			targets, err := t.resolve(x, "", trigger.When.Kind, trigger.When.Name, false)
			if err != nil || targets[0] != g || trigger.When.Type != conditionType || conditionStatus(trigger.When.Status) != status {
				continue
			}
			return membership{trigger: triggerName(j), since: since, period: period}, true
		}
		return membership{}, false
	}}
}

// refuseTrigger records a trigger that cannot run, such as one whose
// condition refers to a resource that does not exist, as a failed run
// with no transition. It posts one Warning for each new reason.
func (o *operator) refuseTrigger(r resource, name string, err error) {
	if run, ok := o.runs.get(r.record(), name); ok && run.Since == nil && run.State == observatory.StepFailed && run.Summary == err.Error() {
		return
	}
	now := stamp()
	o.runs.put(r.record(), observatory.ProcedureRun{Trigger: name, State: observatory.StepFailed, StartTime: &now, StopTime: &now, Summary: err.Error()})
	o.recorder.Warning(reference(r.kind, r.meta), reasonProcedureFailed, fmt.Sprintf("Procedure %s failed: %v", name, err))
}
