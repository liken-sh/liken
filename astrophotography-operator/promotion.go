package main

// The promotion loop of plan 03. It moves each target's frames from the
// outboxes to the target's archive with promotion Jobs, and keeps the
// target's counts and its ArchiveAvailable condition from their reports.
//
// The loop reads only the stores, and the Target's status is the whole
// of what tells it that a frame waits. The runner adds one to the
// outbox's pending in status.outboxes after each exposure, and that
// write wakes the loop (watch.go). So the runner and the loop share no
// memory, and a restart of the operator loses nothing: the next pass
// reads the same status.
//
// For each target, a pass does one of three things:
//
//  1. When the target's next Job has ended, it applies the Job's report
//     to the status (promotionstatus.go). When the Job succeeded and its
//     pod is gone with the report, it deletes the Job, and the next pass
//     starts it again.
//  2. When the next Job runs, it reports a pod that cannot start.
//  3. Otherwise, when an outbox holds frames to move, it creates the
//     next Job, if the target's pacing allows and no other Job uses the
//     outbox claim.
//
// So one Job runs for a target at a time, because one Job writes the
// target's catalog, and one runs for an outbox claim at a time, so the
// link carries one copy at a time.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/promote"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// promotions is the state of the promotion loop. Each field is a cache
// or a fact of this process, and a new process starts without them.
type promotions struct {
	*operator
	// versions is the memo of the Targets whose status the loop wrote.
	versions *memo.Versions
	// image is the operator's own image, which the Jobs run.
	image string
	// started holds each target and outbox claim that this process gave
	// a Job. A claim whose frames all failed starts no Job until a new
	// frame arrives, except once after each start of the operator, so a
	// restart retries a failure that a person fixed.
	started map[string]bool
	metrics *promotionMetrics
}

// The backoff of a pass that failed, such as a write that the API
// server refused. Every other pass waits for a change.
const (
	firstRetry = time.Second
	lastRetry  = time.Minute
)

// promote runs the promotion loop until ctx ends.
func (o *operator) promote(ctx context.Context) {
	p := &promotions{operator: o, versions: o.targetVersions, started: map[string]bool{}, metrics: newPromotionMetrics(o.metrics)}
	retry := firstRetry
	for {
		wake := o.changed.wait()
		var err error
		if o.stores.synced() {
			start := time.Now()
			err = p.pass()
			o.metrics.observePass(astrophotography.TargetKind.Name, time.Since(start), err != nil)
		}
		var again <-chan time.Time
		if err != nil {
			fmt.Fprintf(o.logs, "%s: promotion: %v; trying again in %s\n", managedBy, err, retry)
			again = time.After(retry)
			retry = min(2*retry, lastRetry)
		} else {
			retry = firstRetry
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-again:
		}
	}
}

// world is what one pass reads from the stores.
type world struct {
	jobs         []jobObject
	pods         []podObject
	claims       map[string]claimObject
	reservations []observatory.Reservation
	// cameras holds each namespace's cameras, with the telescope and
	// the train of each.
	cameras map[string]map[string]promote.Camera
}

func (s *stores) view(kind string) informer.View { return s.collections[kind].View() }

func (p *promotions) read() world {
	s := p.stores
	w := world{
		jobs:         informer.CachedList[jobObject](s.view("Job")),
		pods:         informer.CachedList[podObject](s.view("Pod")),
		reservations: informer.CachedList[observatory.Reservation](s.view(observatory.ReservationKind.Name)),
		claims:       map[string]claimObject{},
		cameras:      map[string]map[string]promote.Camera{},
	}
	for _, c := range informer.CachedList[claimObject](s.view(claimKind)) {
		w.claims[c.Metadata.Namespace+"/"+c.Metadata.Name] = c
	}
	telescopes := map[string]string{}
	for _, train := range informer.CachedList[observatory.OpticalTrain](s.view(observatory.OpticalTrainKind.Name)) {
		telescopes[train.Metadata.Namespace+"/"+train.Metadata.Name] = train.Spec.Telescope
	}
	for _, camera := range informer.CachedList[observatory.Camera](s.view(observatory.CameraKind.Name)) {
		namespace := camera.Metadata.Namespace
		if w.cameras[namespace] == nil {
			w.cameras[namespace] = map[string]promote.Camera{}
		}
		w.cameras[namespace][camera.Metadata.Name] = promote.Camera{
			Telescope: telescopes[namespace+"/"+camera.Spec.OpticalTrain], Train: camera.Spec.OpticalTrain,
		}
	}
	return w
}

func targetPath(namespace, name string) string {
	return "/apis/" + astrophotography.APIVersion + "/namespaces/" + namespace + "/" + astrophotography.TargetKind.Plural + "/" + name
}

// pass serves every target once, and then the finalizers of the
// outbox claims.
func (p *promotions) pass() error {
	held := informer.Held{View: p.stores.view(astrophotography.TargetKind.Name), Versions: p.versions}
	targets, err := informer.CurrentList[astrophotography.Target](p.client, held, memo.NamespacedPath(targetPath))
	if err != nil {
		return err
	}
	w := p.read()
	var problems []error
	for i := range targets {
		if err := p.target(&targets[i], w); err != nil {
			problems = append(problems, fmt.Errorf("Target %s/%s: %w", targets[i].Metadata.Namespace, targets[i].Metadata.Name, err))
		}
	}
	problems = append(problems, p.keepFinalizers(targets, w.claims))
	return errors.Join(problems...)
}

// target serves one target: the report of its next Job when that Job
// has ended, the pod of that Job while it runs, or a new Job.
func (p *promotions) target(t *astrophotography.Target, w world) error {
	next := applied(t) + 1
	job, exists := w.promotionJob(t, next)
	if !exists {
		return p.startJob(t, w, next)
	}
	if ended, succeeded := finished(job); ended {
		if succeeded && !w.hasPod(job) {
			return p.rerun(t, job)
		}
		result, ready := resultOf(job, next, succeeded, w.pods)
		if !ready {
			return nil
		}
		landed, err := p.settle(t, result.apply)
		if landed && result.report != nil {
			p.metrics.observe(*result.report)
		}
		return err
	}
	if message, waits := unschedulable(job, w.pods); waits {
		return p.setCondition(t, archiveAvailable(conditions.False, astrophotography.ReasonUnschedulable, message))
	}
	return nil
}

// promotionJob answers the target's promotion Job of a number.
func (w world) promotionJob(t *astrophotography.Target, number int32) (jobObject, bool) {
	index := slices.IndexFunc(w.jobs, func(j jobObject) bool {
		n, ok := promotionNumber(j)
		return ok && n == number && j.Metadata.Namespace == t.Metadata.Namespace && j.Metadata.ownedBy(t.Metadata.UID)
	})
	if index < 0 {
		return jobObject{}, false
	}
	return w.jobs[index], true
}

// hasPod reports whether the store holds a pod of a Job.
func (w world) hasPod(j jobObject) bool {
	return slices.ContainsFunc(w.pods, func(pod podObject) bool {
		return pod.Metadata.Namespace == j.Metadata.Namespace && pod.Metadata.Labels[labelPromotionJob] == j.Metadata.Name
	})
}

// rerun deletes a Job that succeeded and whose pod is gone. The report
// was the pod's termination message, so a person, the garbage
// collector, or a lost node that deleted the pod deleted the report
// too, and no later change brings it back. Each Job reads the whole
// catalog and skips the frames that the catalog holds, so the next
// pass starts the same Job again, and its report gives the counts that
// the lost one held. The Job's pods go with it, because the delete
// propagates in the background; the API's own default for a Job
// orphans them. A Job that is already going, such as one that the TTL
// controller deletes in the foreground, needs no second delete: the
// loop starts it again when the store no longer holds it.
func (p *promotions) rerun(t *astrophotography.Target, j jobObject) error {
	if j.Metadata.DeletionTimestamp != nil {
		return nil
	}
	path := "/apis/batch/v1/namespaces/" + j.Metadata.Namespace + "/jobs/" + j.Metadata.Name
	err := p.client.RequestJSON(http.MethodDelete, path, []byte(`{"propagationPolicy":"Background"}`), nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	message := fmt.Sprintf("the pod of the promotion Job %s is gone with its report, so the Job runs again", j.Metadata.Name)
	fmt.Fprintf(p.logs, "%s: Target %s/%s: %s\n", managedBy, t.Metadata.Namespace, t.Metadata.Name, message)
	p.recorder.Normal(events.ObjectReference{APIVersion: astrophotography.APIVersion, Kind: astrophotography.TargetKind.Name,
		Namespace: t.Metadata.Namespace, Name: t.Metadata.Name, UID: t.Metadata.UID}, reasonReportLost, message)
	return nil
}

// claimBusy reports whether a promotion Job that has not ended uses an
// outbox claim.
func (w world) claimBusy(namespace, claim string) bool {
	return slices.ContainsFunc(w.jobs, func(j jobObject) bool {
		_, promotes := promotionNumber(j)
		ended, _ := finished(j)
		return promotes && !ended && j.Metadata.Namespace == namespace && j.Metadata.Annotations[annotationClaim] == claim
	})
}

// unschedulable answers the scheduler's message for a Job's pod that
// cannot start, such as for two claims that no one node can mount.
func unschedulable(j jobObject, pods []podObject) (string, bool) {
	for _, pod := range pods {
		if pod.Metadata.Labels[labelPromotionJob] != j.Metadata.Name {
			continue
		}
		if c, ok := condition(pod.Status.Conditions, "PodScheduled", "False"); ok && c.Reason == "Unschedulable" {
			return fmt.Sprintf("the pod of the promotion Job %s cannot start: %s", j.Metadata.Name, c.Message), true
		}
	}
	return "", false
}

// startJob creates the target's next Job for the first outbox claim
// that needs one.
func (p *promotions) startJob(t *astrophotography.Target, w world, number int32) error {
	namespace := t.Metadata.Namespace
	for _, entry := range t.Status.Outboxes {
		key := namespace + "/" + t.Metadata.Name + "/" + entry.ClaimName
		needed := entry.Pending > entry.Left || (entry.Pending > 0 && !p.started[key])
		if !needed || waitsForReservation(t, entry.ClaimName, w.reservations) || w.claimBusy(namespace, entry.ClaimName) {
			continue
		}
		if _, exists := w.claims[namespace+"/"+t.Spec.Archive.ClaimName]; !exists {
			message := fmt.Sprintf("the PersistentVolumeClaim %s does not exist in %s", t.Spec.Archive.ClaimName, namespace)
			return p.setCondition(t, archiveAvailable(conditions.False, astrophotography.ReasonClaimMissing, message))
		}
		image, err := p.ownImage()
		if err != nil {
			return err
		}
		job := promotionJob(t, entry.ClaimName, number, entry.Pending, image, w.cameras[namespace])
		body, _ := json.Marshal(job)
		err = p.client.RequestJSON(http.MethodPost, "/apis/batch/v1/namespaces/"+namespace+"/jobs", body, nil)
		if err != nil && !errors.Is(err, apiclient.ErrConflict) {
			return err
		}
		p.started[key] = true
		fmt.Fprintf(p.logs, "%s: started the promotion Job %s/%s for %s in %s\n", managedBy, namespace, job.Metadata.Name, frames(entry.Pending), entry.ClaimName)
		return nil
	}
	return nil
}

// waitsForReservation reports whether a target that promotes after the
// reservation waits for one that still runs on the outbox claim: it is
// activating, ready, or deactivating, so the telescope may still work.
func waitsForReservation(t *astrophotography.Target, claim string, reservations []observatory.Reservation) bool {
	if t.Spec.Archive.Promotion != astrophotography.PromotionAfterReservation {
		return false
	}
	return slices.ContainsFunc(reservations, func(r observatory.Reservation) bool {
		outbox := r.Spec.Outbox
		if r.Status.Session != nil && r.Status.Session.Outbox != nil {
			outbox = r.Status.Session.Outbox
		}
		running := r.Status.Phase == observatory.ReservationActivating || r.Status.Phase == observatory.ReservationReady ||
			r.Status.Phase == observatory.ReservationDeactivating
		return r.Metadata.Namespace == t.Metadata.Namespace && running && outbox != nil && outbox.ClaimName == claim
	})
}

// setCondition writes one condition, when it differs from the status.
func (p *promotions) setCondition(t *astrophotography.Target, next conditions.Condition) error {
	_, err := p.settle(t, func(held *astrophotography.Target) ([]conditions.Condition, bool) {
		before, _ := conditions.Find(held.Status.Conditions, next.Type)
		changed := setCondition(held, nil, next)
		after, _ := conditions.Find(held.Status.Conditions, next.Type)
		return changed, before != after
	})
	return err
}

// settle writes the status that apply composes, and posts the Event of
// each condition that transitioned once the write lands.
func (p *promotions) settle(t *astrophotography.Target, apply func(*astrophotography.Target) ([]conditions.Condition, bool)) (bool, error) {
	var changed []conditions.Condition
	path := targetPath(t.Metadata.Namespace, t.Metadata.Name)
	t.APIVersion, t.Kind = astrophotography.APIVersion, astrophotography.TargetKind.Name
	landed, err := informer.SettleStatus[astrophotography.Target](p.client, p.versions, path, t, func(held *astrophotography.Target) bool {
		var needed bool
		changed, needed = apply(held)
		return needed
	})
	if landed {
		reference := events.ObjectReference{APIVersion: astrophotography.APIVersion, Kind: astrophotography.TargetKind.Name,
			Namespace: t.Metadata.Namespace, Name: t.Metadata.Name, UID: t.Metadata.UID}
		for _, c := range changed {
			p.recorder.Transition(reference, c, badStatus(c))
		}
	}
	return landed, err
}

// badStatus answers the status of a condition that needs a person:
// ArchiveAvailable False. Complete needs no person either way.
func badStatus(c conditions.Condition) conditions.Status {
	if c.Type == astrophotography.ConditionArchiveAvailable {
		return conditions.False
	}
	return ""
}
