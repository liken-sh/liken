package main

// One run of one procedure: the actions of one trigger of one
// resource, in order. A run records the transition time of the
// condition it answers, so a trigger runs once for each transition,
// and a run that an operator restart interrupted resumes from its
// record: each action that is not Done runs again. The actions are
// target states, so running one again changes only what is still
// needed.
//
// A resource runs one procedure at a time, the lifecycle's included.
// A run that becomes due while another run of the resource goes on
// waits for it, and its record is Pending with the trigger it waits
// for.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The reasons of a run's Events: one when it starts, and one when it
// ends.
const (
	reasonProcedureStarted = "ProcedureStarted"
	reasonProcedureDone    = "ProcedureDone"
	reasonProcedureFailed  = "ProcedureFailed"
)

// membership is the run of one resource that answers an event: its
// trigger, the transition time it answers, and the start of the
// resource's activity, which a trigger's run must not predate.
type membership struct {
	trigger       string
	since, period time.Time
}

// event is what an action's after waits on: the runs that answer the
// same lifecycle step, or the same transition of the same condition.
// member answers the run of a resource that answers the event, and
// false for a resource with no such run, which after does not wait for.
type event struct {
	member func(t *tree, key string) (membership, bool)
	// over answers why the event ended, such as a condition that
	// changed, or nil while it holds. Nil for a lifecycle step. A run
	// that waited for its turn starts only while its event holds, and
	// after does not wait for a run that never began before the event
	// ended, because none will begin.
	over func(t *tree) error
}

// procCall is one run of one trigger of one resource.
type procCall struct {
	res     resource
	trigger string
	actions []action
	// since is the transition time that the run answers.
	since time.Time
	// period is the start of the resource's current activity, or zero.
	// A run that started before it answered the same transition in an
	// earlier activity, so the trigger runs again.
	period time.Time
	// rerun runs a Failed run of the same transition again, as a
	// lifecycle step does, so the retry annotation runs its actions
	// again.
	rerun bool
	// ending skips each action on a device that is not connected, as
	// deactivation does after an activation that failed before it
	// connected every device.
	ending bool
	event  event
}

// answers reports whether a run answers a transition in an activity
// that began at period.
func answers(run observatory.ProcedureRun, since, period time.Time) bool {
	if run.Since == nil || !run.Since.Equal(since) {
		return false
	}
	return period.IsZero() || (run.StartTime != nil && !run.StartTime.Before(period))
}

func ended(state observatory.StepState) bool {
	return state == observatory.StepDone || state == observatory.StepSkipped
}

// sameActions reports whether a run's record holds the actions that
// the spec states now. A spec that changed starts a new run.
func sameActions(run observatory.ProcedureRun, actions []action) bool {
	return slices.EqualFunc(run.Actions, actions, func(r observatory.ActionRun, a action) bool { return r.Action == a.String() })
}

// settled reports whether the run of a call's transition has ended,
// with the error of a run that failed and that the call does not run
// again.
func (o *operator) settled(c procCall) (bool, error) {
	run, found := o.runs.get(c.res.key(), c.trigger)
	if !found || !answers(run, c.since, c.period) || !sameActions(run, c.actions) {
		return false, nil
	}
	switch {
	case ended(run.State):
		return true, nil
	case run.State == observatory.StepFailed && !c.rerun:
		return true, fmt.Errorf("%s: %s", c.res, lowerFirst(run.Summary))
	}
	return false, nil
}

// runProcedure runs one trigger's actions, and answers an error that
// names the resource when an action fails. A run that answered the
// transition already is not run again, unless it failed and the call
// reruns it. A run that started runs to its end: only its context
// ends it, when the resource's deactivation begins or the operator
// stops.
func (o *operator) runProcedure(ctx context.Context, c procCall) error {
	if len(c.actions) == 0 {
		return nil
	}
	if done, err := o.settled(c); done {
		return err
	}
	key := c.res.key()
	pending := false
	err := o.runs.acquire(ctx, key, c.trigger, func(holder string) {
		if run, found := o.runs.get(key, c.trigger); found && answers(run, c.since, c.period) && run.State == observatory.StepRunning {
			// A run that an operator restart interrupted keeps its
			// record while it waits.
			return
		}
		pending = true
		o.runs.put(key, pendingRun(c, holder))
	})
	if err != nil {
		if pending && !errors.Is(err, context.Canceled) {
			// The deactivation ended the run's wait. An operator that
			// stops leaves the record Pending, and the next copy starts
			// the run again.
			o.runs.put(key, skippedRun(c, err))
		}
		return err
	}
	defer o.runs.release(key)
	if done, err := o.settled(c); done {
		return err
	}
	run, found := o.runs.get(key, c.trigger)
	same := found && answers(run, c.since, c.period) && sameActions(run, c.actions)
	if resumed := same && run.State == observatory.StepRunning; !resumed && c.event.over != nil {
		if why := c.event.over(o.snapshot()); why != nil {
			o.runs.put(key, skippedRun(c, why))
			return nil
		}
	}
	switch {
	case same && run.State == observatory.StepRunning:
		// An operator restart interrupted the run: it resumes, and
		// posts no second start.
	case same:
		now := stamp()
		run.State, run.StartTime, run.StopTime, run.Summary = observatory.StepRunning, &now, nil, ""
		for i := range run.Actions {
			if a := &run.Actions[i]; !ended(a.State) {
				a.State, a.StartTime, a.StopTime, a.Summary = observatory.StepPending, nil, nil, ""
			}
		}
		o.procedureStarted(c)
	default:
		now, since := stamp(), c.since
		run = observatory.ProcedureRun{Trigger: c.trigger, Since: &since, State: observatory.StepRunning, StartTime: &now}
		for _, a := range c.actions {
			run.Actions = append(run.Actions, observatory.ActionRun{Action: a.String(), State: observatory.StepPending})
		}
		o.procedureStarted(c)
	}
	o.runs.put(key, run)
	for i := range run.Actions {
		if ended(run.Actions[i].State) {
			continue
		}
		if err := o.runAction(ctx, c, &run, i); err != nil {
			return err
		}
	}
	stop := stamp()
	run.State, run.StopTime = observatory.StepDone, &stop
	var did []string
	skippedAll := true
	for _, a := range run.Actions {
		did = append(did, lowerFirst(a.Summary))
		skippedAll = skippedAll && a.State == observatory.StepSkipped
	}
	if skippedAll {
		run.State = observatory.StepSkipped
	}
	run.Summary = sentence(strings.Join(did, "; "))
	o.runs.put(key, run)
	o.recorder.Normal(reference(c.res.kind, c.res.meta), reasonProcedureDone,
		fmt.Sprintf("Procedure %s %s in %s: %s", c.trigger, strings.ToLower(string(run.State)), duration(stop.Sub(*run.StartTime)), lowerFirst(run.Summary)))
	return nil
}

// pendingRun is the record of a run that waits for the run of another
// trigger of its resource to end.
func pendingRun(c procCall, holder string) observatory.ProcedureRun {
	since := c.since
	run := observatory.ProcedureRun{Trigger: c.trigger, Since: &since, State: observatory.StepPending,
		Summary: "Waiting for the run of " + holder + " to end"}
	for _, a := range c.actions {
		run.Actions = append(run.Actions, observatory.ActionRun{Action: a.String(), State: observatory.StepPending})
	}
	return run
}

// skippedRun is the record of a run that waited for its turn while its
// condition changed, so it never began.
func skippedRun(c procCall, why error) observatory.ProcedureRun {
	run := pendingRun(c, "")
	now := stamp()
	run.State, run.StopTime, run.Summary = observatory.StepSkipped, &now, sentence(why.Error())+", so the run did not begin"
	for i := range run.Actions {
		run.Actions[i].State = observatory.StepSkipped
	}
	return run
}

func (o *operator) procedureStarted(c procCall) {
	var described []string
	for _, a := range c.actions {
		described = append(described, a.String())
	}
	o.recorder.Normal(reference(c.res.kind, c.res.meta), reasonProcedureStarted,
		fmt.Sprintf("Procedure %s started: %s", c.trigger, strings.Join(described, ", ")))
}

// runAction runs one action of a run, and records its state in the
// run as it goes.
func (o *operator) runAction(ctx context.Context, c procCall, run *observatory.ProcedureRun, i int) error {
	key, a := c.res.key(), c.actions[i]
	record := &run.Actions[i]
	now := stamp()
	record.State, record.StartTime, record.StopTime, record.Summary = observatory.StepRunning, &now, nil, ""
	o.runs.put(key, *run)
	last := ""
	report := func(message string) {
		last = message
		if record.Summary != sentence(message) {
			record.Summary = sentence(message)
			o.runs.put(key, *run)
		}
	}
	limit, err := a.timeout()
	summary := ""
	if err == nil {
		wait, cancel := context.WithTimeout(ctx, limit)
		summary, err = o.perform(ctx, wait, c, a, report)
		if err != nil && ctx.Err() == nil && errors.Is(wait.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("%s after %s: %s", strings.ToLower(timedOut), duration(limit), lowerFirst(firstNonEmpty(last, err.Error())))
		}
		cancel()
	}
	stop := stamp()
	record.StopTime = &stop
	switch {
	case err == nil:
		record.State, record.Summary = observatory.StepDone, sentence(summary)
	case isSkip(err):
		record.State, record.Summary = observatory.StepSkipped, sentence(err.Error())
	case ctx.Err() != nil && errors.Is(context.Cause(ctx), context.Canceled):
		// The operator stops. The run stays Running, and the next copy
		// of the operator resumes it.
		return ctx.Err()
	case ctx.Err() != nil:
		// The run's work ended for a reason that is no failure, such as
		// a reservation that ended during its Activation step, or the
		// deactivation of the resource while a trigger's run went on.
		cause := err
		if ctx.Err() != nil {
			cause = context.Cause(ctx)
		}
		why := sentence(cause.Error())
		record.State, record.Summary = observatory.StepSkipped, why
		run.State, run.StopTime, run.Summary = observatory.StepSkipped, &stop, why
		o.runs.put(key, *run)
		return cause
	default:
		record.State, record.Summary = observatory.StepFailed, sentence(err.Error())
		run.State, run.StopTime = observatory.StepFailed, &stop
		run.Summary = a.String() + ": " + err.Error()
		o.runs.put(key, *run)
		o.recorder.Warning(reference(c.res.kind, c.res.meta), reasonProcedureFailed,
			fmt.Sprintf("Procedure %s failed: %s", c.trigger, lowerFirst(run.Summary)))
		return fmt.Errorf("%s: %s", c.res, run.Summary)
	}
	o.runs.put(key, *run)
	return nil
}

// perform waits for what an action requires and for the runs it comes
// after, and then carries it out. wait holds the action's deadline.
func (o *operator) perform(ctx, wait context.Context, c procCall, a action, report func(string)) (string, error) {
	if err := o.requirements(wait, c, a, report); err != nil {
		return "", err
	}
	if err := o.predecessors(wait, c, a, report); err != nil {
		return "", err
	}
	if c.res.device == nil {
		return "", fmt.Errorf("a %s has no %s", c.res.kind.Name, a)
	}
	h, err := o.deviceHandle(wait, c, report)
	if err != nil {
		return "", err
	}
	return o.act(ctx, wait, h, a, report)
}

// asNow answers the resource of a call as the tree holds it now, so
// a reference resolves against the tree's current shape.
func asNow(t *tree, c procCall) resource {
	if r, ok := t.resource(c.res.kind, c.res.name()); ok {
		return r
	}
	return c.res
}

// conditionStatus answers the status that a reference asks for: True
// when it names none.
func conditionStatus(s observatory.ConditionStatus) observatory.ConditionStatus {
	if s == "" {
		return observatory.ConditionTrue
	}
	return s
}

// requirements waits until each condition that the action requires
// holds.
func (o *operator) requirements(ctx context.Context, c procCall, a action, report func(string)) error {
	if len(a.Requires) == 0 {
		return nil
	}
	return o.waitFor(ctx, report, func(t *tree) (bool, string, error) {
		from := asNow(t, c)
		for i, req := range a.Requires {
			targets, err := t.resolve(from, fmt.Sprintf("requires[%d]", i), req.Kind, req.Name, false)
			if err != nil {
				return false, "", err
			}
			conditions, _ := t.conditionsOf(targets[0].kind, targets[0].name)
			want := conditionStatus(req.Status)
			if conditionOf(conditions, req.Type).Status != want {
				return false, fmt.Sprintf("waiting for %s %s=%s", targets[0], req.Type, want), nil
			}
		}
		return true, "", nil
	})
}

// predecessors waits until the run of each resource that the action
// comes after has ended, for the same event. A resource with no such
// run is not waited for, and a run that failed fails the action.
func (o *operator) predecessors(ctx context.Context, c procCall, a action, report func(string)) error {
	if len(a.After) == 0 || c.event.member == nil {
		return nil
	}
	return o.waitFor(ctx, report, func(t *tree) (bool, string, error) {
		from := asNow(t, c)
		for i, ref := range a.After {
			targets, err := t.resolve(from, fmt.Sprintf("after[%d]", i), ref.Kind, ref.Name, true)
			if err != nil {
				return false, "", err
			}
			for _, g := range targets {
				m, ok := c.event.member(t, g.key())
				if !ok || g.key() == from.key() {
					continue
				}
				run, found := o.runs.get(g.key(), m.trigger)
				answered := found && answers(run, m.since, m.period)
				switch {
				case answered && run.State == observatory.StepFailed:
					return false, "", fmt.Errorf("after[%d]: %s failed: %s", i, g, lowerFirst(run.Summary))
				case answered && ended(run.State):
					continue
				case !answered && c.event.over != nil && c.event.over(t) != nil:
					// The event ended before this resource's run of it
					// began, so that run never begins.
					continue
				}
				return false, "waiting for " + g.String(), nil
			}
		}
		return true, "", nil
	})
}

// deviceHandle answers the device of a call on its server, connected.
// A run that ends the device's activity skips a device that is not
// connected, and any other run fails on one.
func (o *operator) deviceHandle(ctx context.Context, c procCall, report func(string)) (handle, error) {
	t := o.snapshot()
	d, ok := t.device(c.res.kind, c.res.name())
	if !ok {
		return handle{}, fmt.Errorf("%s is gone", c.res)
	}
	ref, placed := t.server(d)
	if !placed {
		return handle{}, skip(fmt.Sprintf("%s is on the shelf", c.res))
	}
	if c.ending {
		live, _ := o.connectedHandles(ctx, report, t, ref, []*device{d})
		if len(live) == 0 {
			return handle{}, skip(fmt.Sprintf("%s is not connected", c.res))
		}
		return live[0], nil
	}
	byKey, err := o.waitDefined(ctx, report, ref, []*device{d})
	if err != nil {
		return handle{}, err
	}
	h := byKey[d.key()]
	if !h.connected() {
		return handle{}, fmt.Errorf("%s is not connected", c.res)
	}
	return h, nil
}
