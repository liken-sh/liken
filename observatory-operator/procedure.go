package main

// A run of a procedure: the actions of one trigger of one resource,
// run in order. A run records the transition time of the condition
// that started it, so a trigger runs once for each transition. When an
// operator restart interrupts a run, the run resumes from its record
// and runs each action that has not ended. The actions describe target
// states, so running one again changes only what is still needed.
//
// A resource runs one procedure at a time, including its activation
// and deactivation. A run that becomes due while another run of the
// same resource is in progress waits for it, and its record is Pending
// and names the trigger of the run it waits for.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// Reasons of a run's Events. The message of reasonProcedureDone says
// whether the run was Done or Skipped.
const (
	reasonProcedureStarted = "ProcedureStarted"
	reasonProcedureDone    = "ProcedureDone"
	reasonProcedureFailed  = "ProcedureFailed"
)

// membership is the trigger, since, and period that find one
// resource's run record for an event. since and period mean the same
// as in procCall.
type membership struct {
	trigger       string
	since, period time.Time
}

// An event groups the runs that one change starts across several
// resources: one lifecycle step, or one transition of one condition.
// An action's `after` waits only for runs of the same event. member
// returns a resource's run for the event, or false when the resource
// has no run in it, and then `after` does not wait for that resource.
type event struct {
	member func(t *tree, key string) (membership, bool)
	// over returns why the event ended, such as a condition that
	// changed back, or nil while it continues. It is nil for a
	// lifecycle step. A run that has not begun when its event ends
	// never begins, so `after` does not wait for it.
	over func(t *tree) error
}

// procCall is one run of one trigger of one resource.
type procCall struct {
	res     resource
	trigger string
	// condition is the condition of a trigger in spec.triggers, such
	// as "WeatherStation lab Safe=False", and "" for activation and
	// deactivation. Summaries and Events show it, because a trigger's
	// name, such as triggers[0], tells a person nothing.
	condition string
	actions   []action
	// since is the time of the condition transition that started the
	// run.
	since time.Time
	// period is the start of the resource's current activity, or zero.
	// A record of the same transition that started before period
	// belongs to an earlier activity, so the trigger runs again.
	period time.Time
	// rerun starts a Failed run of the same transition again. The
	// lifecycle steps always set it, because the session decides when a
	// step runs again. A trigger and a late activation set it only when
	// the resource has the retry annotation.
	rerun bool
	// ending makes the run skip each action on a device that is not
	// connected. Deactivation sets it, because an activation can fail
	// before it connects every device.
	ending bool
	// on is the server to act through when the device has left it but
	// its driver still runs there (leaves.go). When on is zero, the run
	// acts through the device's own server.
	on    serverRef
	event event
}

func (c procCall) label() string {
	if c.condition == "" {
		return c.trigger
	}
	return c.trigger + " (" + c.condition + ")"
}

func (c procCall) summary(text string) string {
	if c.condition == "" {
		return sentence(text)
	}
	return "When " + c.condition + ": " + lowerFirst(text)
}

// isRunFor reports whether run is the record of the run for the
// transition at since, in the activity that began at period. A zero
// period matches any activity.
func isRunFor(run observatory.ProcedureRun, since, period time.Time) bool {
	if run.Since == nil || !run.Since.Equal(since) {
		return false
	}
	return period.IsZero() || (run.StartTime != nil && !run.StartTime.Before(period))
}

func ended(state observatory.StepState) bool {
	return state == observatory.StepDone || state == observatory.StepSkipped
}

// sameActions reports whether a run's record lists the actions that the
// spec has now. When the actions changed, a new run starts.
func sameActions(run observatory.ProcedureRun, actions []action) bool {
	return slices.EqualFunc(run.Actions, actions, func(r observatory.ActionRun, a action) bool { return r.Action == a.String() })
}

// settled reports whether the run for the call's transition needs no
// more work: it ended, or it failed and rerun is false. For a failed
// run it returns the failure.
func (o *operator) settled(c procCall) (bool, error) {
	run, found := o.runs.get(c.res.record(), c.trigger)
	if !found || !isRunFor(run, c.since, c.period) || !sameActions(run, c.actions) {
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

// runProcedure runs the actions of one trigger, and returns an error
// that names the resource when an action fails. A run that has begun
// does not stop when its event ends. Only its context stops it, when
// the resource's deactivation begins or the operator stops.
func (o *operator) runProcedure(ctx context.Context, c procCall) error {
	if len(c.actions) == 0 {
		return nil
	}
	if done, err := o.settled(c); done {
		return err
	}
	key := c.res.record()
	pending := false
	err := o.runs.acquire(ctx, key, c.label(), func(holder string) {
		if run, found := o.runs.get(key, c.trigger); found && isRunFor(run, c.since, c.period) && run.State == observatory.StepRunning {
			// An operator restart interrupted this run. Its record stays
			// Running while it waits, so it resumes.
			return
		}
		pending = true
		o.runs.put(key, pendingRun(c, holder))
	})
	if err != nil {
		if pending && !errors.Is(err, context.Canceled) {
			// The wait ended before the run began, for example because
			// the resource's deactivation began. When the operator is
			// stopping instead, the record stays Pending and the next
			// operator process starts the run.
			o.runs.put(key, skippedRun(c, err))
		}
		return err
	}
	defer o.runs.release(key)
	if done, err := o.settled(c); done {
		return err
	}
	run, found := o.runs.get(key, c.trigger)
	same := found && isRunFor(run, c.since, c.period) && sameActions(run, c.actions)
	// A run that resumes after a restart has begun already, so it
	// continues even when its event has ended.
	if resumed := same && run.State == observatory.StepRunning; !resumed && c.event.over != nil {
		if why := c.event.over(o.snapshot()); why != nil {
			o.runs.put(key, skippedRun(c, why))
			return nil
		}
	}
	switch {
	case same && run.State == observatory.StepRunning:
		// The run resumes after a restart, with no second start Event.
	case same:
		// A Pending or Failed run starts. Actions that ended keep their
		// state and do not run again.
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
	text := strings.Join(did, "; ")
	run.Summary = c.summary(text)
	o.runs.put(key, run)
	o.recorder.Normal(reference(c.res.kind, c.res.meta), reasonProcedureDone,
		fmt.Sprintf("Procedure %s %s in %s: %s", c.label(), strings.ToLower(string(run.State)), duration(stop.Sub(*run.StartTime)), lowerFirst(text)))
	return nil
}

// pendingRun returns the record of a run that is waiting for the run of
// another trigger of the same resource to end.
func pendingRun(c procCall, holder string) observatory.ProcedureRun {
	since := c.since
	run := observatory.ProcedureRun{Trigger: c.trigger, Since: &since, State: observatory.StepPending,
		Summary: c.summary("waiting for the run of " + holder + " to end")}
	for _, a := range c.actions {
		run.Actions = append(run.Actions, observatory.ActionRun{Action: a.String(), State: observatory.StepPending})
	}
	return run
}

// skippedRun returns the record of a run that never began. why is the
// reason.
func skippedRun(c procCall, why error) observatory.ProcedureRun {
	run := pendingRun(c, "")
	now := stamp()
	run.State, run.StopTime, run.Summary = observatory.StepSkipped, &now, c.summary(why.Error()+", so the run did not begin")
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
		fmt.Sprintf("Procedure %s started: %s", c.label(), strings.Join(described, ", ")))
}

// runAction runs action i of a run, and writes the action's progress
// into run's record.
func (o *operator) runAction(ctx context.Context, c procCall, run *observatory.ProcedureRun, i int) error {
	key, a := c.res.record(), c.actions[i]
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
		summary, err = o.perform(ctx, wait, c, a, place{index: i, start: *run.StartTime, limit: limit}, report)
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
		// The operator is stopping. The run stays Running, and the
		// next operator process resumes it.
		return ctx.Err()
	case ctx.Err() != nil:
		// The run's context ended for a reason that is not a failure,
		// such as a reservation that ended during its Activation step,
		// or the resource's deactivation starting during a trigger's
		// run. The run is Skipped with that reason.
		cause := err
		if ctx.Err() != nil {
			cause = context.Cause(ctx)
		}
		record.State, record.Summary = observatory.StepSkipped, sentence(cause.Error())
		run.State, run.StopTime, run.Summary = observatory.StepSkipped, &stop, c.summary(cause.Error())
		o.runs.put(key, *run)
		return cause
	default:
		record.State, record.Summary = observatory.StepFailed, sentence(err.Error())
		run.State, run.StopTime = observatory.StepFailed, &stop
		// The summary starts with the action as the YAML spells it,
		// such as "state: Parked", so it is not capitalized.
		text := a.String() + ": " + err.Error()
		run.Summary = text
		if c.condition != "" {
			run.Summary = c.summary(text)
		}
		o.runs.put(key, *run)
		o.recorder.Warning(reference(c.res.kind, c.res.meta), reasonProcedureFailed,
			fmt.Sprintf("Procedure %s failed: %s", c.label(), lowerFirst(text)))
		return fmt.Errorf("%s: %s", c.res, text)
	}
	o.runs.put(key, *run)
	return nil
}

// place locates an action within its run, for runJob. index is the
// action's position in the run, and runJob puts it in the Job's name.
// start is the time the run started: a Job with that name created
// before start belongs to an earlier run, so runJob deletes it. limit
// is the action's deadline, and runJob sets it on the Job.
type place struct {
	index int
	start time.Time
	limit time.Duration
}

// perform waits for the action's `requires` and `after`, and then
// performs the action. wait is ctx with the action's deadline.
func (o *operator) perform(ctx, wait context.Context, c procCall, a action, at place, report func(string)) (string, error) {
	if err := o.requirements(wait, c, a, report); err != nil {
		return "", err
	}
	if err := o.predecessors(wait, c, a, report); err != nil {
		return "", err
	}
	if a.Job != nil {
		// A job needs no device, so any resource can run one.
		return o.runJob(wait, c, *a.Job, at, report)
	}
	if a.Notify != "" {
		// A notify action needs no device either. It is done once the
		// run records it, and the status writer sets AttentionNeeded
		// from the record (attention.go).
		return "notified: " + a.Notify, nil
	}
	if c.res.device == nil {
		return "", fmt.Errorf("a %s has no %s", c.res.kind.Name, a)
	}
	h, err := o.deviceHandle(wait, c, report)
	if err != nil {
		return "", err
	}
	summary, err := o.act(ctx, wait, h, a, report)
	return summary, o.explainRefusal(c, h, a, err)
}

// asNow returns the call's resource from the current tree, so that
// `requires` and `after` resolve against the current specs. A call with
// `on` set keeps the resource as it was, because the device has left
// its server and the run still acts there.
func asNow(t *tree, c procCall) resource {
	if c.on != (serverRef{}) {
		return c.res
	}
	if r, ok := t.resource(c.res.kind, c.res.name()); ok {
		return r
	}
	return c.res
}

// conditionStatus returns the status that a reference requires. It
// defaults to True.
func conditionStatus(s observatory.ConditionStatus) observatory.ConditionStatus {
	if s == "" {
		return observatory.ConditionTrue
	}
	return s
}

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

// predecessors waits until the run of each resource in the action's
// `after` has ended, for the same event. It skips the resource itself
// and each resource with no run for the event, and fails the action
// when one of those runs failed.
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
				x, _ := t.resource(g.kind, g.name)
				run, found := o.runs.get(x.record(), m.trigger)
				recorded := found && isRunFor(run, m.since, m.period)
				switch {
				case recorded && run.State == observatory.StepFailed:
					return false, "", fmt.Errorf("after[%d]: %s failed: %s", i, g, lowerFirst(run.Summary))
				case recorded && ended(run.State):
					continue
				case !recorded && c.event.over != nil && c.event.over(t) != nil:
					// The event ended before this resource's run began,
					// so that run never begins.
					continue
				}
				return false, "waiting for " + g.String(), nil
			}
		}
		return true, "", nil
	})
}

// deviceHandle returns the connected INDI device that serves the
// call's resource. It skips the action when the operator refuses to
// serve the resource, when the resource has no server, and when the
// device does not have the resource's INDI interface. A refusal and a
// missing interface already show as Error in the resource's status,
// so a skip lets the rest of the telescope continue. When the device
// is not connected, a run with `ending` set skips the action, and any
// other run fails it.
func (o *operator) deviceHandle(ctx context.Context, c procCall, report func(string)) (handle, error) {
	t := o.snapshot()
	d, ok := t.device(c.res.kind, c.res.name())
	if !ok {
		return handle{}, fmt.Errorf("%s is gone", c.res)
	}
	ref, placed := t.server(d)
	if c.on != (serverRef{}) {
		ref, placed = c.on, true
	} else if refusal := t.ownershipOf(d).refusal; refusal != "" {
		return handle{}, skip(fmt.Sprintf("%s is not served: %s", c.res, refusal))
	}
	if !placed && c.ending {
		// A device that a person deleted, or whose OpticalTrain a
		// person deleted, has no server, but its driver still runs on
		// the server it left, and deactivation acts through it.
		ref, placed = t.runningOn(d)
	}
	if !placed {
		return handle{}, skip(fmt.Sprintf("%s is not installed", c.res))
	}
	var h handle
	if c.ending {
		live, _ := o.connectedHandles(ctx, report, t, ref, []*device{d})
		if len(live) == 0 {
			return handle{}, skip(fmt.Sprintf("%s is not connected", c.res))
		}
		h = live[0]
	} else {
		byKey, err := o.waitDefined(ctx, report, ref, []*device{d})
		if err != nil {
			return handle{}, err
		}
		h = byKey[d.key()]
		if !h.connected() {
			return handle{}, fmt.Errorf("%s is not connected", c.res)
		}
	}
	// After an operator restart, the server sends each device's
	// definitions again, and an interface's properties can arrive after
	// CONNECTION, so detect waits for them.
	if !o.detect(ctx, h) {
		return handle{}, skip(firstNonEmpty(o.lackingOf(h.d), missingInterface(h).Error()))
	}
	return h, nil
}
