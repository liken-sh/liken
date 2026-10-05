package main

// A runner runs one Reservation's steps, one at a time, in the order of
// observatory.ActivationSteps and then observatory.DeactivationSteps.
// The reservation's status is the runner's record: each step's state,
// start, finish, and message. A runner that starts reads that record
// and continues from the first step that is not Done or Skipped, so an
// operator that restarts in the middle of a step runs the step again.
// Each step reads what the cluster and the devices report before it
// changes anything, so a step that runs again changes only what is
// still needed.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// errEnding is the cause that ends an activation step when the
// reservation is deleted or reaches spec.end.
var errEnding = errors.New("the reservation ended before this step finished")

type runner struct {
	o    *operator
	uid  string
	name string
	// res is the last copy of the reservation read from the stores. Its
	// status is not the record: the runner writes the status, and
	// status holds what it wrote last.
	res    *observatory.Reservation
	status observatory.ReservationStatus
	done   chan struct{}
}

func newRunner(o *operator, r *observatory.Reservation) *runner {
	return &runner{o: o, uid: r.Metadata.UID, name: r.Metadata.Name, res: r, status: r.Status, done: make(chan struct{})}
}

// stage is what the record says the runner does next.
type stage int

const (
	stageActivate stage = iota
	stageReady
	stageDeactivate
	stageFailed
	stageReleased
)

func (r *runner) run(ctx context.Context) {
	for ctx.Err() == nil {
		if !r.refresh() {
			// The reservation is gone. Its telescope goes back to the
			// pool, and the supervisor's sweep stops any pod it left.
			r.o.claims.release(r.res)
			return
		}
		if !hasFinalizer(r.res) && r.res.Metadata.DeletionTimestamp == nil && r.status.Phase != observatory.ReservationReleased {
			if err := r.o.setFinalizer(r.res, true); err != nil {
				r.retryLater(ctx, "adding the finalizer", err)
				continue
			}
			r.awaitStore(ctx, hasFinalizer)
			continue
		}
		if len(r.status.Steps) == 0 {
			r.status.Steps = pendingSteps(observatory.ActivationSteps)
			r.status.Phase = observatory.ReservationScheduled
			r.save(ctx)
		}
		switch r.stage() {
		case stageActivate:
			r.activate(ctx)
		case stageReady:
			r.steady(ctx)
		case stageDeactivate:
			r.deactivate(ctx)
		case stageFailed:
			r.failed(ctx)
		case stageReleased:
			r.released(ctx)
			return
		}
	}
}

// refresh reads the reservation, and answers false when it is gone. It
// reads the store's copy, unless the copy is older than the operator's
// own last write, and then it reads the API server's (informer.ReadOne).
func (r *runner) refresh() bool {
	key := r.o.namespace + "/" + r.name
	held := informer.Held{View: r.o.stores.kinds[observatory.ReservationKind].View(), Versions: r.o.versions}
	res, err := informer.ReadOne[observatory.Reservation](r.o.client, held, key, objectPath(observatory.ReservationKind, r.o.namespace, r.name))
	switch {
	case errors.Is(err, apiclient.ErrNotFound):
		return false
	case err != nil:
		// The API server does not answer. The runner keeps its last
		// copy, and its next write meets the same failure and is
		// logged.
		return true
	case res.Metadata.UID != r.uid:
		return false
	}
	r.res = res
	return true
}

// ending reports whether the reservation must deactivate: a person
// deleted it, or spec.end came.
func ending(res *observatory.Reservation, now time.Time) bool {
	return res.Metadata.DeletionTimestamp != nil || (res.Spec.End != nil && !now.Before(*res.Spec.End))
}

func (r *runner) stage() stage {
	if r.status.Phase == observatory.ReservationReleased {
		return stageReleased
	}
	deactivating := false
	for _, s := range r.status.Steps {
		if isDeactivation(s.Name) {
			deactivating = true
		}
		if s.State == observatory.StepFailed && (deactivating || !ending(r.res, time.Now())) {
			return stageFailed
		}
	}
	switch {
	case deactivating && allFinished(r.status.Steps):
		return stageReleased
	case deactivating, ending(r.res, time.Now()):
		return stageDeactivate
	case allFinished(r.status.Steps):
		return stageReady
	}
	return stageActivate
}

func isDeactivation(name observatory.StepName) bool {
	for _, d := range observatory.DeactivationSteps {
		if d == name {
			return true
		}
	}
	return false
}

func pendingSteps(names []observatory.StepName) []observatory.Step {
	var steps []observatory.Step
	for _, name := range names {
		steps = append(steps, observatory.Step{Name: name, State: observatory.StepPending})
	}
	return steps
}

// allFinished reports whether every step is Done or Skipped.
func allFinished(steps []observatory.Step) bool {
	for _, s := range steps {
		if s.State != observatory.StepDone && s.State != observatory.StepSkipped {
			return false
		}
	}
	return true
}

func (r *runner) step(name observatory.StepName) *observatory.Step {
	for i := range r.status.Steps {
		if r.status.Steps[i].Name == name {
			return &r.status.Steps[i]
		}
	}
	return nil
}

// stepWork is what a step function receives: the runner, and a way to
// say what the step waits for now.
type stepWork struct {
	r *runner
	// report records what the step does or waits for in the step's
	// message, and writes the status when the message changed.
	report func(string)
	// last is the last message that report received.
	last string
}

// outcome is how a step ended.
type outcome struct {
	skipped bool
	message string
}

func done(format string, args ...any) (outcome, error) {
	return outcome{message: fmt.Sprintf(format, args...)}, nil
}

func skipped(format string, args ...any) (outcome, error) {
	return outcome{skipped: true, message: fmt.Sprintf(format, args...)}, nil
}

type stepFunc func(ctx context.Context, w *stepWork) (outcome, error)

// runStep runs one step from its record. A Pending step starts now; a
// Running step continues with its first start time, so its deadline
// stays where it was before an operator restart.
func (r *runner) runStep(ctx context.Context, name observatory.StepName, fn stepFunc) error {
	s := r.step(name)
	now := stamp()
	if s.State == observatory.StepPending {
		s.State, s.StartTime, s.FinishTime, s.Message = observatory.StepRunning, &now, nil, ""
	}
	r.status.Step = name
	r.status.Phase = phaseOf(name)
	r.save(ctx)

	limit := stepTimeouts[name]
	stepCtx, cancel := ctx, context.CancelFunc(func() {})
	if limit > 0 {
		stepCtx, cancel = context.WithDeadline(ctx, s.StartTime.Add(limit))
	}
	defer cancel()
	work := &stepWork{r: r}
	work.report = func(message string) {
		work.last = message
		if r.step(name).Message != message {
			r.step(name).Message = message
			r.save(ctx)
		}
	}
	result, err := fn(stepCtx, work)

	s = r.step(name)
	finish := stamp()
	s.FinishTime = &finish
	switch {
	case err != nil && errors.Is(context.Cause(ctx), errEnding):
		s.State, s.Message = observatory.StepSkipped, errEnding.Error()
		r.save(ctx)
		return errEnding
	case err != nil && ctx.Err() != nil:
		// The operator stops. The step stays Running, and the next
		// copy of the operator continues it.
		s.FinishTime = nil
		return ctx.Err()
	case err != nil && errors.Is(stepCtx.Err(), context.DeadlineExceeded):
		s.State = observatory.StepFailed
		s.Message = fmt.Sprintf("timed out after %v: %s", limit, firstNonEmpty(work.last, err.Error()))
		r.failure(ctx, name, reasonTimedOut, s.Message)
		return err
	case err != nil:
		s.State, s.Message = observatory.StepFailed, err.Error()
		r.failure(ctx, name, reasonFailed, s.Message)
		return err
	case result.skipped:
		s.State, s.Message = observatory.StepSkipped, result.message
	default:
		s.State, s.Message = observatory.StepDone, result.message
	}
	r.save(ctx)
	r.o.record(r.res, eventNormal, string(name), fmt.Sprintf("%s %s: %s", name, s.State, s.Message))
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// The reasons of a failed step, in the Ready condition and the Event.
const (
	reasonFailed   = "StepFailed"
	reasonTimedOut = "StepTimedOut"
)

func (r *runner) failure(ctx context.Context, name observatory.StepName, reason, message string) {
	r.status.Phase = observatory.ReservationFailed
	r.save(ctx)
	r.o.record(r.res, eventWarning, reason, fmt.Sprintf("%s: %s", name, message))
}

// phaseOf answers the phase while a step runs.
func phaseOf(name observatory.StepName) observatory.ReservationPhase {
	switch {
	case name == observatory.StepWait:
		return observatory.ReservationScheduled
	case isDeactivation(name):
		return observatory.ReservationDeactivating
	}
	return observatory.ReservationActivating
}

// retryLater logs a failed write and waits before the runner reads the
// reservation again. The wait is a clock that spaces the writes while
// the API server refuses them.
func (r *runner) retryLater(ctx context.Context, what string, err error) {
	fmt.Fprintf(os.Stderr, "observatory-operator: the Reservation %s: %s: %v\n", r.name, what, err)
	timer := time.NewTimer(retryPause)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// retryPause is the wait after a write that the API server refused.
const retryPause = 5 * time.Second

// save writes the record to the reservation's status, with the phase's
// conditions and endpoint, and writes nothing when the status holds it
// already. A failed write is retried with the next save, and the record
// in memory stays as it is.
func (r *runner) save(ctx context.Context) {
	r.compose()
	held := *r.res
	_, err := informer.SettleStatus[observatory.Reservation](r.o.client.WithContext(ctx), r.o.versions,
		objectPath(observatory.ReservationKind, held.Metadata.Namespace, held.Metadata.Name), &held,
		func(copy *observatory.Reservation) bool {
			next := r.status
			next.Conditions = mergeConditions(copy.Status.Conditions, r.status.Conditions)
			if equalJSON(copy.Status, next) {
				return false
			}
			copy.APIVersion, copy.Kind, copy.Status = observatory.APIVersion, observatory.ReservationKind.Name, next
			return true
		})
	if err == nil {
		r.res = &held
	} else if ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "observatory-operator: writing the status of the Reservation %s: %v\n", r.name, err)
	}
}

// compose sets the conditions, the endpoint, and the observed
// generation that the phase gives.
func (r *runner) compose() {
	s := &r.status
	s.ObservedGeneration = r.res.Metadata.Generation
	s.Endpoint = nil
	ready := condition(observatory.ConditionReady, observatory.ConditionFalse, string(s.Phase), phaseMessage(s))
	safe := condition(observatory.ConditionSafeToPowerOff, observatory.ConditionFalse, string(s.Phase), "the deactivation steps have not finished")
	switch s.Phase {
	case observatory.ReservationReady:
		endpoint := r.endpoint()
		s.Endpoint = &endpoint
		ready = condition(observatory.ConditionReady, observatory.ConditionTrue, "Ready",
			fmt.Sprintf("connect KStars to %s:%d", endpoint.Host, endpoint.Port))
	case observatory.ReservationReleased:
		safe = condition(observatory.ConditionSafeToPowerOff, observatory.ConditionTrue, "Released",
			"the deactivation steps are done, and the devices are safe to power off")
	case observatory.ReservationFailed:
		for _, step := range s.Steps {
			if step.State == observatory.StepFailed {
				reason := reasonFailed
				if len(step.Message) > 9 && step.Message[:9] == "timed out" {
					reason = reasonTimedOut
				}
				ready = condition(observatory.ConditionReady, observatory.ConditionFalse, reason, fmt.Sprintf("%s: %s", step.Name, step.Message))
				safe.Reason, safe.Message = reason, ready.Message
			}
		}
	}
	s.Conditions = []observatory.Condition{ready, safe}
	for i := range s.Conditions {
		s.Conditions[i].ObservedGeneration = s.ObservedGeneration
	}
}

func phaseMessage(s *observatory.ReservationStatus) string {
	if step := findStep(s.Steps, s.Step); step != nil && step.Message != "" {
		return fmt.Sprintf("%s: %s", step.Name, step.Message)
	}
	return string(s.Phase)
}

func findStep(steps []observatory.Step, name observatory.StepName) *observatory.Step {
	for i := range steps {
		if steps[i].Name == name {
			return &steps[i]
		}
	}
	return nil
}

func (r *runner) endpoint() observatory.Endpoint {
	name := serverRef{observatory.TelescopeKind, r.res.Spec.Telescope}.String()
	return observatory.Endpoint{Service: name, Host: serviceHost(name, r.o.namespace), Port: serverPort}
}
