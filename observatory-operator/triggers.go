package main

// The triggers of every resource, in spec.triggers. One controller
// reads every trigger after each change of a store or of a run's
// record. A trigger runs its actions once for each transition of its
// condition to the status it names: the run records the condition's
// lastTransitionTime from the stored status, and a condition that
// keeps its status keeps that time. A trigger runs only while its
// resource is active: from the end of its activation run, or from the
// start of its Telescope's or Observatory's activity when it has no
// activation, until its deactivation begins. So a condition that holds
// when the activation ends runs the trigger then.
//
// A run that started runs to its end, whatever its condition does
// meanwhile. A weather station that flaps, or that reconnects and
// reports Safe as Unknown for a moment, must not stop a park halfway.
// Only the resource's deactivation, or the operator's stop, ends a
// run. A resource runs one procedure at a time (procedure.go), so a
// run that becomes due while another run of the resource goes on waits
// for it. It then begins only while its condition still holds with the
// same transition time, and its record otherwise says why it did not
// begin.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// triggerName names the trigger of one item of spec.triggers, such as
// triggers[0].
func triggerName(i int) string { return fmt.Sprintf("triggers[%d]", i) }

// flight is one trigger's run that goes on now.
type flight struct {
	cancel context.CancelCauseFunc
	done   chan struct{}
}

// keepTriggers runs the triggers after each change, until ctx ends.
func (o *operator) keepTriggers(ctx context.Context) {
	flights := map[string]*flight{}
	var group sync.WaitGroup
	defer group.Wait()
	for {
		wake := o.structure.wait()
		var due time.Time
		if o.stores.ready() && o.seeded.Load() {
			due = o.evaluateTriggers(ctx, o.snapshot(), flights, &group)
		}
		// The timer is a clock: the end of a trigger's for, when its
		// run becomes due.
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

// evaluateTriggers starts each trigger's run that is due, stops each
// run whose resource stopped being active, and answers the earliest
// time a trigger's for ends, or zero.
func (o *operator) evaluateTriggers(ctx context.Context, t *tree, flights map[string]*flight, group *sync.WaitGroup) time.Time {
	for id, f := range flights {
		select {
		case <-f.done:
			delete(flights, id)
		default:
		}
	}
	var due time.Time
	for _, r := range t.withTriggers() {
		period, active := o.activePeriod(r)
		for i, trigger := range r.procedures.Triggers {
			name := triggerName(i)
			id := r.key() + "/" + name
			f := flights[id]
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
			if run, ok := o.runs.get(r.key(), name); ok && run.State == observatory.StepRunning && run.Since != nil && answers(run, *run.Since, period) {
				// An operator restart interrupted the run, and it
				// resumes, whatever its condition reads now.
				call.since, call.event = *run.Since, o.conditionEvent(r, targets[0], when.Type, want, *run.Since)
				flights[id] = o.fly(ctx, call, group)
				continue
			}
			if !holds {
				continue
			}
			if run, ok := o.runs.get(r.key(), name); ok && answers(run, since, period) {
				continue
			}
			if at := since.Add(wait); time.Now().Before(at) {
				if due.IsZero() || at.Before(due) {
					due = at
				}
				continue
			}
			call.since, call.event = since, o.conditionEvent(r, targets[0], when.Type, want, since)
			flights[id] = o.fly(ctx, call, group)
		}
	}
	return due
}

// conditionText names a trigger's condition as a person reads it, such
// as "WeatherStation lab Safe=False" or "WeatherStation lab Safe=True
// for 20m".
func conditionText(g target, when observatory.When) string {
	text := fmt.Sprintf("%s %s=%s", g, when.Type, conditionStatus(when.Status))
	if when.For != "" {
		text += " for " + when.For
	}
	return text
}

// fly starts one trigger's run.
func (o *operator) fly(ctx context.Context, call procCall, group *sync.WaitGroup) *flight {
	runCtx, cancel := context.WithCancelCause(ctx)
	f := &flight{cancel: cancel, done: make(chan struct{})}
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

// withTriggers answers every resource that has a trigger in
// spec.triggers.
func (t *tree) withTriggers() []resource {
	var out []resource
	add := func(r resource) {
		if len(r.procedures.Triggers) > 0 {
			out = append(out, r)
		}
	}
	for _, name := range sortedNames(t.observatories) {
		add(ofObservatory(t.observatories[name]))
	}
	for _, name := range sortedNames(t.telescopes) {
		add(ofTelescope(t.telescopes[name]))
	}
	for _, d := range t.devices {
		add(t.ofDevice(d))
	}
	return out
}

// activePeriod answers when a resource's activity began, and false
// while it is not active: while its Telescope or Observatory is not
// Active, and until its activation run for that transition is Done.
func (o *operator) activePeriod(r resource) (time.Time, bool) {
	var key string
	switch {
	case r.kind == observatory.ObservatoryKind:
		key = observatoryKey(r.name())
	case r.kind == observatory.TelescopeKind:
		key = telescopeKey(r.name())
	case r.telescope != "":
		key = telescopeKey(r.telescope)
	case r.observatory != "":
		key = observatoryKey(r.observatory)
	default:
		return time.Time{}, false
	}
	state := o.activity.get(key)
	if !state.active {
		return time.Time{}, false
	}
	if len(r.procedures.Activation) > 0 {
		run, ok := o.runs.get(r.key(), observatory.TriggerActivation)
		if !ok || !answers(run, state.since, time.Time{}) || !ended(run.State) {
			return time.Time{}, false
		}
	}
	return state.since, true
}

// conditionEvent answers the transition of one condition to one status
// as the event that each action's after waits on: the runs of the
// other resources' triggers that answer the same transition. For the
// run of r, the event is also over when r's activity ended. The
// activity ends before the controller stops r's runs, one by one, so
// a run that waited for its turn behind a run that just stopped does
// not begin.
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
			return fmt.Errorf("%s %s turned %s again at %s", g, conditionType, status, c.LastTransitionTime.UTC().Format(time.RFC3339))
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
// condition names a resource that does not exist, as a failed run with
// no transition. It posts one Warning for each new reason.
func (o *operator) refuseTrigger(r resource, name string, err error) {
	if run, ok := o.runs.get(r.key(), name); ok && run.Since == nil && run.State == observatory.StepFailed && run.Summary == err.Error() {
		return
	}
	now := stamp()
	o.runs.put(r.key(), observatory.ProcedureRun{Trigger: name, State: observatory.StepFailed, StartTime: &now, StopTime: &now, Summary: err.Error()})
	o.recorder.Warning(reference(r.kind, r.meta), reasonProcedureFailed, fmt.Sprintf("Procedure %s failed: %v", name, err))
}
