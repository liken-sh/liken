package main

// A runner runs one Reservation's steps in order: first
// observatory.ActivationSteps, then observatory.DeactivationSteps. The
// Reservation's status records each step's state, start time, stop
// time, and message. A new runner reads that status and continues from
// the first step that is not Done or Skipped, so an operator that
// restarts during a step runs that step again. Each step reads the
// state of the cluster and the devices before it changes anything, so
// a step that runs again changes only what is still missing.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// errEnding is the context cause that stops an activation step when
// ending reports that the reservation must deactivate.
var errEnding = errors.New("reservation ended")

type runner struct {
	o    *operator
	uid  string
	name string
	// res is the last copy of the reservation that the runner read.
	// After the first refresh, the runner does not read its status from
	// res, because status is what the runner last wrote.
	res    *observatory.Reservation
	status observatory.ReservationStatus
	// logged is the last phase that the runner wrote to the log.
	logged observatory.ReservationPhase
	// session is the EditsPending condition that updateEditsPending last set,
	// or nil when there is none.
	session *observatory.Condition
	// guiderLink has the UIDs of the guider's pod and the INDI server's
	// pod when the runner last saw PHD2 with its equipment connected.
	// When either pod changes, the runner checks PHD2's equipment again
	// (guidersteady.go).
	guiderLink string
}

func newRunner(o *operator, r *observatory.Reservation) *runner {
	return &runner{o: o, uid: r.Metadata.UID, name: r.Metadata.Name, res: r, status: r.Status, logged: r.Status.Phase}
}

// stage is what the runner does next, decided from the status.
type stage int

const (
	stageActivate stage = iota
	stageReady
	stageDeactivate
	stageFailed
	stageReleased
)

func (r *runner) run(ctx context.Context) {
	// The supervisor starts a runner from its snapshot, whose copy of the
	// reservation can be older than the operator's last write. When the
	// previous runner has just released the reservation and removed its
	// finalizer, a runner that used the older copy would add the
	// finalizer and release again, in a loop with no delay. So the runner
	// takes its status from its first refresh, which is never older than
	// the operator's last write.
	first := true
	for ctx.Err() == nil {
		if !r.refresh() {
			// The reservation is deleted. Free its telescope. The
			// supervisor's sweep stops any pod that it left.
			r.o.claims.release(r.res)
			return
		}
		if first {
			r.status, r.logged, first = r.res.Status, r.res.Status.Phase, false
			r.status.Steps = renameStoredSteps(r.res.Status.Steps)
			r.session = storedSession(r.res.Status)
			if finished(r.res) {
				// A finished reservation has no step to run, but a person
				// can still edit it, so observe updates
				// status.observedGeneration (see edited).
				r.observe(ctx)
				return
			}
		}
		if !hasFinalizer(r.res) && r.res.Metadata.DeletionTimestamp == nil && r.status.Phase != observatory.ReservationReleased {
			if err := r.o.send(ctx, nil, "adding the finalizer of the Reservation "+r.name, func() error {
				r.refresh()
				return r.o.setFinalizer(r.res, true)
			}); err != nil {
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
			r.clearStep(ctx)
			r.steady(ctx)
		case stageDeactivate:
			r.deactivate(ctx)
		case stageFailed:
			r.failed(ctx)
		case stageReleased:
			r.clearStep(ctx)
			r.released(ctx)
			return
		}
	}
}

// refresh reads the reservation into r.res and returns false when the
// reservation is deleted or replaced by one with another UID. It uses
// the informer's copy, unless that copy is older than the operator's
// last write, and then it reads from the API server (informer.ReadOne).
func (r *runner) refresh() bool {
	key := r.o.namespace + "/" + r.name
	held := informer.Held{View: r.o.stores.kinds[observatory.ReservationKind].View(), Versions: r.o.versions}
	res, err := informer.ReadOne[observatory.Reservation](r.o.client, held, key, objectPath(observatory.ReservationKind, r.o.namespace, r.name))
	switch {
	case errors.Is(err, apiclient.ErrNotFound):
		return false
	case err != nil:
		// Keep the last copy. When the API server is still
		// unavailable, the next status write fails and logs it.
		return true
	case res.Metadata.UID != r.uid:
		return false
	}
	r.res = res
	return true
}

// ending reports whether the reservation must deactivate: it is being
// deleted, spec.end has passed, or the telescope it holds or that
// telescope's observatory is being deleted (see deletedParent).
func ending(t *tree, res *observatory.Reservation, now time.Time) bool {
	return deleting(res.Metadata) || (res.Spec.End != nil && !now.Before(*res.Spec.End)) || heldParentDeleted(t, res) != ""
}

// heldParentDeleted returns the deleted parent of the reservation's
// telescope, such as "Telescope east", when the reservation holds the
// telescope, and "" otherwise. Before it holds the telescope, the
// activation step reports a deleted parent (activation.go).
func heldParentDeleted(t *tree, res *observatory.Reservation) string {
	telescope, ok := t.telescopes[res.Spec.Telescope]
	if !ok || !holds(res) {
		return ""
	}
	return deletedParent(t, telescope)
}

// deletedParent returns "Telescope <name>" when the telescope is being
// deleted, "Observatory <name>" when its observatory is being deleted,
// or "". Finalizers keep both objects in the tree until the
// reservation that holds the telescope is Released.
func deletedParent(t *tree, telescope *observatory.Telescope) string {
	if deleting(telescope.Metadata) {
		return "Telescope " + telescope.Metadata.Name
	}
	if site, ok := t.observatories[telescope.Spec.Observatory]; ok && deleting(site.Metadata) {
		return "Observatory " + site.Metadata.Name
	}
	return ""
}

func (r *runner) stage() stage {
	if r.status.Phase == observatory.ReservationReleased {
		return stageReleased
	}
	t := r.o.snapshot()
	deactivating := false
	for _, s := range r.status.Steps {
		if isDeactivation(s.Name) {
			deactivating = true
		}
		if s.State == observatory.StepFailed && (deactivating || !ending(t, r.res, time.Now())) {
			return stageFailed
		}
	}
	switch {
	case deactivating && allFinished(r.status.Steps):
		return stageReleased
	case deactivating, ending(t, r.res, time.Now()), endedAtDawn(r.status.Steps):
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

// renameStoredSteps returns a copy of the stored steps with the step
// named Abort renamed StopWork. A status written by an older operator
// during a deactivation can have the name Abort. Without the rename,
// deactivate finds no StopWork step and adds every deactivation step
// again, so the status lists each of them twice. The slice is copied
// because the stored one belongs to the informer's cache.
func renameStoredSteps(stored []observatory.Step) []observatory.Step {
	steps := slices.Clone(stored)
	for i := range steps {
		if steps[i].Name == observatory.StepStoredAbort {
			steps[i].Name = observatory.StepStopWork
		}
	}
	return steps
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

type stepWork struct {
	r    *runner
	name observatory.StepName
	// report sets the step's summary to a message that says what the
	// step is doing or waiting for. It writes the status when the
	// summary changed or the spec was edited (see edited).
	report func(string)
	// last is the last message that report received. A step that times
	// out gives it as the reason.
	last string
}

// outcome is the result of a step that returned no error. A skipped
// step needed no work. message becomes the step's summary.
type outcome struct {
	skipped bool
	message string
}

func done(format string, args ...any) (outcome, error) {
	return outcome{message: sentence(fmt.Sprintf(format, args...))}, nil
}

func skipped(format string, args ...any) (outcome, error) {
	return outcome{skipped: true, message: sentence(fmt.Sprintf(format, args...))}, nil
}

type stepFunc func(ctx context.Context, w *stepWork) (outcome, error)

// runStep runs one step from its status. A Pending step starts now. A
// Running step keeps its original start time, so its deadline does not
// move when the operator restarts.
func (r *runner) runStep(ctx context.Context, name observatory.StepName, fn stepFunc) error {
	s := r.step(name)
	now := stamp()
	begins := "resumed"
	if s.State == observatory.StepPending {
		s.State, s.StartTime, s.StopTime, s.Summary = observatory.StepRunning, &now, nil, ""
		begins = "started"
	}
	r.status.Step = name
	r.status.Phase = phaseOf(name)
	r.save(ctx)
	r.o.logf("Reservation %s: %s %s", r.name, name, begins)

	limit := stepTimeouts[name]
	stepCtx, cancel := ctx, context.CancelFunc(func() {})
	if limit > 0 {
		stepCtx, cancel = context.WithDeadline(ctx, s.StartTime.Add(limit))
	}
	defer cancel()
	work := &stepWork{r: r, name: name}
	work.report = func(message string) {
		message = sentence(message)
		work.last = message
		if r.step(name).Summary != message || r.edited() {
			r.step(name).Summary = message
			r.save(ctx)
		}
	}
	result, err := fn(stepCtx, work)

	s = r.step(name)
	finish := stamp()
	s.StopTime = &finish
	dawn, atDawn := asDawnError(err)
	switch {
	case errors.Is(err, errSiteChanged):
		// The step waits again before it starts (sitestop.go).
		s.State, s.StartTime, s.StopTime, s.Summary = observatory.StepPending, nil, nil, ""
		r.save(ctx)
		return err
	case atDawn:
		// Dawn came before the step's requirement was met
		// (requires.go). Mark the step Skipped with what it waited
		// for, so that stage returns stageDeactivate.
		s.State, s.Summary = observatory.StepSkipped, dawn.summary
		r.save(ctx)
		r.o.logf("Reservation %s: %s", r.name, stepLine(s))
		r.o.record(r.res, string(name), sentence(strings.TrimPrefix(stepLine(s), string(name)+" ")))
		return errEnding
	case err != nil && errors.Is(context.Cause(ctx), errEnding):
		s.State, s.Summary = observatory.StepSkipped, sentence(errEnding.Error())
		r.save(ctx)
		r.o.logf("Reservation %s: %s", r.name, stepLine(s))
		return errEnding
	case err != nil && ctx.Err() != nil:
		// The operator is stopping. Leave the step Running, so the next
		// operator process continues it.
		s.StopTime = nil
		return ctx.Err()
	case err != nil && errors.Is(stepCtx.Err(), context.DeadlineExceeded):
		s.State = observatory.StepFailed
		s.Summary = fmt.Sprintf("%s after %s: %s", timedOut, duration(limit), lowerFirst(firstNonEmpty(work.last, err.Error())))
		r.failure(ctx, s, reasonTimedOut)
		return err
	case err != nil:
		s.State, s.Summary = observatory.StepFailed, "Failed: "+err.Error()
		r.failure(ctx, s, reasonFailed)
		return err
	case result.skipped:
		s.State, s.Summary = observatory.StepSkipped, result.message
	default:
		s.State, s.Summary = observatory.StepDone, result.message
	}
	r.save(ctx)
	r.o.logf("Reservation %s: %s", r.name, stepLine(s))
	r.o.record(r.res, string(name), sentence(strings.TrimPrefix(stepLine(s), string(name)+" ")))
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

// The reasons of a failed step, for the Ready condition and the Event.
const (
	reasonFailed   = "StepFailed"
	reasonTimedOut = "StepTimedOut"
)

func (r *runner) failure(ctx context.Context, s *observatory.Step, reason string) {
	r.o.logf("Reservation %s: %s", r.name, stepLine(s))
	r.o.series.stepFailed(r.res.Spec.Telescope, s.Name)
	r.status.Phase = observatory.ReservationFailed
	r.save(ctx)
	r.o.recorder.Warning(reference(observatory.ReservationKind, r.res.Metadata), reason, failedMessage(s))
}

// clearStep empties status.step, so the printer column does not show a
// step that ended.
func (r *runner) clearStep(ctx context.Context) {
	if r.status.Step != "" {
		r.status.Step = ""
		r.save(ctx)
	}
}

// began returns the earliest start time of the named steps, or now.
func (r *runner) began(names []observatory.StepName) time.Time {
	first := time.Now()
	for _, s := range r.status.Steps {
		if slices.Contains(names, s.Name) && s.StartTime != nil && s.StartTime.Before(first) {
			first = *s.StartTime
		}
	}
	return first
}

// took returns how long a finished step ran, or zero.
func (r *runner) took(name observatory.StepName) time.Duration {
	if s := r.step(name); s != nil && s.StartTime != nil && s.StopTime != nil {
		return s.StopTime.Sub(*s.StartTime)
	}
	return 0
}

// phaseOf returns the reservation's phase while the step runs.
func phaseOf(name observatory.StepName) observatory.ReservationPhase {
	switch {
	case name == observatory.StepWait:
		return observatory.ReservationScheduled
	case isDeactivation(name):
		return observatory.ReservationDeactivating
	}
	return observatory.ReservationActivating
}

// save writes the runner's status, with the conditions and endpoint
// that compose sets, and writes nothing when the stored status is
// equal. After a failed write, the next save writes the status again.
// A caller that must know the write succeeded, such as recordSession,
// reads the error.
func (r *runner) save(ctx context.Context) error {
	r.compose()
	if r.status.Phase != r.logged {
		r.logged = r.status.Phase
		r.o.logf("Reservation %s: %s", r.name, phaseLine(&r.status))
	}
	held := *r.res
	var transitions []observatory.Condition
	wrote, err := informer.SettleStatus[observatory.Reservation](r.o.client.WithContext(ctx), r.o.versions,
		objectPath(observatory.ReservationKind, held.Metadata.Namespace, held.Metadata.Name), &held,
		func(copy *observatory.Reservation) bool {
			next := r.status
			next.Conditions, transitions = mergeConditions(copy.Status.Conditions, r.status.Conditions)
			if equalJSON(copy.Status, next) {
				return false
			}
			copy.APIVersion, copy.Kind, copy.Status = observatory.APIVersion, observatory.ReservationKind.Name, next
			return true
		})
	if wrote {
		// Ready and SafeToPowerOff change only when a step ends or the
		// phase changes, and the runner posts an Event for each of
		// those (events.go). EditsPending changes when a person edits
		// a spec, so save posts its Event.
		for _, c := range transitions {
			if c.Type == observatory.ConditionEditsPending {
				r.o.recorder.Transition(reference(observatory.ReservationKind, r.res.Metadata), c, "")
			}
		}
	}
	if err == nil {
		r.res = &held
	} else if ctx.Err() == nil {
		r.o.logf("writing the status of the Reservation %s: %v", r.name, err)
	}
	return err
}

// edited reports whether the spec changed since the runner last wrote
// the status. A reservation that is Ready, Failed, Released, or in the
// Wait step changes no step, so without a write for the edit alone,
// status.observedGeneration stays behind metadata.generation and
// `kubectl wait` cannot tell that the operator read the edit. The
// write records that the operator read the edit, not that it applied
// it, because an edit during a session takes effect at the next
// session (records.go).
func (r *runner) edited() bool {
	return r.status.ObservedGeneration != r.res.Metadata.Generation
}

// observe writes the status when the spec changed since the last write
// (see edited), or when updateEditsPending changed the EditsPending
// condition. An edit of an Observatory, a Telescope, or a Camera does
// not change the reservation's generation, so the runner's waits call
// observe each time o.structure notifies, which every watch event does.
func (r *runner) observe(ctx context.Context) {
	if judged := r.updateEditsPending(r.o.snapshot()); judged || r.edited() {
		r.save(ctx)
	}
}

// compose sets the conditions, the endpoint, and the observed
// generation of the status from the current phase and step.
func (r *runner) compose() {
	s := &r.status
	s.ObservedGeneration = r.res.Metadata.Generation
	s.Endpoint = nil
	reason, message := string(s.Phase), string(s.Phase)
	if step := findStep(s.Steps, s.Step); step != nil {
		reason, message = string(step.Name), firstNonEmpty(step.Summary, "Running")
	}
	ready := condition(observatory.ConditionReady, observatory.ConditionFalse, reason, message)
	// SafeToPowerOff is False until the Released phase. Its reason and
	// message say why: activation is waiting or running, the holder is
	// using the telescope, deactivation is running, or a step failed.
	telescope := "Telescope " + r.res.Spec.Telescope
	safe := condition(observatory.ConditionSafeToPowerOff, observatory.ConditionFalse, string(s.Phase), string(s.Phase)+" "+telescope)
	switch s.Phase {
	case observatory.ReservationScheduled:
		safe.Message = "Waiting to activate " + telescope
	case observatory.ReservationReady:
		endpoint := r.endpoint()
		s.Endpoint = &endpoint
		ready = condition(observatory.ConditionReady, observatory.ConditionTrue, "Ready",
			fmt.Sprintf("Ready at %s:%d", endpoint.Host, endpoint.Port))
		safe.Reason, safe.Message = "InUse", "In use by "+r.res.Spec.Holder
	case observatory.ReservationReleased:
		safe = condition(observatory.ConditionSafeToPowerOff, observatory.ConditionTrue, "Released", "Safe to power off")
	case observatory.ReservationFailed:
		for i := range s.Steps {
			if step := &s.Steps[i]; step.State == observatory.StepFailed {
				reason := reasonFailed
				if strings.HasPrefix(step.Summary, timedOut) {
					reason = reasonTimedOut
				}
				ready = condition(observatory.ConditionReady, observatory.ConditionFalse, reason, failedMessage(step))
				safe.Reason, safe.Message = reason, ready.Message
			}
		}
	}
	s.Conditions = []observatory.Condition{ready, safe}
	if r.session != nil && s.Phase != observatory.ReservationReleased {
		s.Conditions = append(s.Conditions, *r.session)
	}
	for i := range s.Conditions {
		s.Conditions[i].ObservedGeneration = s.ObservedGeneration
	}
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
