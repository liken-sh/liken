package main

// The promotion loop runs promotion Jobs that move each Target's frames
// from the outbox claims to the Target's archive claim, and sets the
// Target's frame counts and its ArchiveAvailable condition from the
// Jobs' reports.
//
// The loop reads the frames that wait in an outbox only from the
// Target's status.outboxes. The runner increments that count after each
// exposure, and the status write wakes the loop. Because the count is in
// the status and not in memory, a restart of the operator loses nothing.
//
// For each Target, a pass first updates status.sequences,
// status.progress, and the Complete condition from the spec, so a new
// Target shows its progress before a reservation claims it. Then the
// pass does one of these:
//
//  1. When the Target's next Job has ended, it applies the Job's report
//     to the status (promotionstatus.go). When the Job succeeded but its
//     pod, and so its report, is gone, it deletes the Job, and the next
//     pass creates it again.
//  2. When the next Job is running, it reports a pod that cannot be
//     scheduled.
//  3. Otherwise it handles the retry annotation (promotionretry.go), and
//     creates the next Job when an outbox holds more frames than the
//     last Job left there.
//
// At most one Job runs for a Target, because a Job writes the Target's
// catalog. At most one Job runs for an outbox claim, so a network
// archive receives one copy at a time.

import (
	"bytes"
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

// promotions is the state of the promotion loop. Every field is a cache,
// so a new process starts with none of it.
type promotions struct {
	*operator
	// versions records the resourceVersion of each Target that the
	// operator last read or wrote, and jobs records it for each Job that
	// this loop created.
	versions, jobs *memo.Versions
	// image is the operator's own image, which the Jobs run.
	image   string
	metrics *promotionMetrics
}

// After a failed pass, the loop runs again after a delay that doubles
// from firstRetry up to lastRetry. After a pass that succeeds, it waits
// for the next change.
const (
	firstRetry = time.Second
	lastRetry  = time.Minute
)

// promote runs the promotion loop until ctx ends.
func (o *operator) promote(ctx context.Context) {
	p := &promotions{operator: o, versions: o.targetVersions, jobs: memo.New(), metrics: newPromotionMetrics(o.metrics)}
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
	// cameras maps each namespace's Camera names to the telescope and
	// optical train of each.
	cameras map[string]map[string]promote.Camera
	// created has each outbox claim, as namespace/name, that a Job
	// created in this pass uses. jobs was listed before the pass created
	// any Job, so without created, a second Target with the same outbox
	// claim would start a second Job on it in the same pass.
	created map[string]bool
}

func (s *stores) view(kind string) informer.View { return s.collections[kind].View() }

func (p *promotions) read() (world, error) {
	jobs, err := p.currentJobs()
	if err != nil {
		return world{}, err
	}
	s := p.stores
	w := world{
		jobs:         jobs,
		pods:         informer.CachedList[podObject](s.view("Pod")),
		reservations: informer.CachedList[observatory.Reservation](s.view(observatory.ReservationKind.Name)),
		claims:       map[string]claimObject{},
		cameras:      map[string]map[string]promote.Camera{},
		created:      map[string]bool{},
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
	return w, nil
}

// currentJobs lists the Jobs, including each Job that the loop created
// and the watch has not delivered to the store yet, so a pass does not
// create a Job twice. It then removes from p.jobs each Job that is in
// neither the list nor the store.
func (p *promotions) currentJobs() ([]jobObject, error) {
	store := p.storeOf("Job", p.jobs)
	store.View.Whole = true
	jobs, err := informer.CurrentList[jobObject](p.client, store, memo.NamespacedPath(jobPath))
	if err != nil {
		return nil, err
	}
	listed := make(map[string]bool, len(jobs))
	for i := range jobs {
		listed[memo.Key(&jobs[i].Metadata.ObjectMeta)] = true
	}
	p.jobs.ForgetGone(store.View.Store, listed)
	return jobs, nil
}

func jobPath(namespace, name string) string {
	return "/apis/batch/v1/namespaces/" + namespace + "/jobs/" + name
}

func targetPath(namespace, name string) string {
	return "/apis/" + astrophotography.APIVersion + "/namespaces/" + namespace + "/" + astrophotography.TargetKind.Plural + "/" + name
}

// pass handles every Target once, including its finalizer, and then
// handles the finalizers of the outbox claims.
func (p *promotions) pass() error {
	store := informer.Held{View: p.stores.view(astrophotography.TargetKind.Name), Versions: p.versions}
	targets, err := informer.CurrentList[astrophotography.Target](p.client, store, memo.NamespacedPath(targetPath))
	if err != nil {
		return err
	}
	w, err := p.read()
	if err != nil {
		return err
	}
	var problems []error
	for i := range targets {
		err := p.target(&targets[i], w)
		if err == nil {
			err = p.keepTargetFinalizer(&targets[i], w)
		}
		if err != nil {
			problems = append(problems, fmt.Errorf("promoting Target %s/%s: %w", targets[i].Metadata.Namespace, targets[i].Metadata.Name, err))
		}
	}
	problems = append(problems, p.keepFinalizers(targets, w.claims))
	return errors.Join(problems...)
}

func (p *promotions) target(t *astrophotography.Target, w world) error {
	if err := p.progress(t); err != nil {
		return err
	}
	next := applied(t) + 1
	job, exists := w.promotionJob(t, next)
	if !exists {
		if err := p.retry(t); err != nil {
			return err
		}
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
		written, err := p.settle(t, result.apply)
		if written && result.report != nil {
			p.metrics.observe(*result.report)
		}
		return err
	}
	if message, waits := unschedulable(job, w.pods); waits {
		return p.setCondition(t, archiveAvailable(conditions.False, astrophotography.ReasonUnschedulable, message))
	}
	return nil
}

// progress writes status.sequences, status.progress, and the Complete
// condition from the spec and the counts already in the status, so an
// edit of spec.sequences shows in the status before the next report.
func (p *promotions) progress(t *astrophotography.Target) error {
	_, err := p.settle(t, func(current *astrophotography.Target) ([]conditions.Condition, bool) {
		before, _ := json.Marshal(current.Status)
		alignSequences(current)
		changed := setCondition(current, nil, completeness(current))
		after, _ := json.Marshal(current.Status)
		return changed, !bytes.Equal(before, after)
	})
	return err
}

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

func (w world) hasPod(j jobObject) bool {
	return slices.ContainsFunc(w.pods, func(pod podObject) bool {
		return pod.Metadata.Namespace == j.Metadata.Namespace && pod.Metadata.Labels[labelPromotionJob] == j.Metadata.Name
	})
}

// rerun deletes a Job that succeeded and whose pod is gone. The report
// is the pod's termination message, so it is lost with the pod. The
// next pass creates the same Job again, and because a report's totals
// come from the whole catalog, the new report restores the lost counts.
// The delete sets background propagation because the API's default for
// a Job orphans its pods. A Job that is already being deleted, for
// example by the TTL controller, needs no second delete.
func (p *promotions) rerun(t *astrophotography.Target, j jobObject) error {
	if j.Metadata.DeletionTimestamp != nil {
		return nil
	}
	err := p.client.RequestJSON(http.MethodDelete, jobPath(j.Metadata.Namespace, j.Metadata.Name), []byte(`{"propagationPolicy":"Background"}`), nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	message := fmt.Sprintf("Deleted the promotion Job %s to run it again, because its pod was deleted before the operator read its report", j.Metadata.Name)
	fmt.Fprintf(p.logs, "%s: Target %s/%s: %s\n", managedBy, t.Metadata.Namespace, t.Metadata.Name, message)
	p.recorder.Normal(events.ObjectReference{APIVersion: astrophotography.APIVersion, Kind: astrophotography.TargetKind.Name,
		Namespace: t.Metadata.Namespace, Name: t.Metadata.Name, UID: t.Metadata.UID}, reasonReportLost, message)
	return nil
}

// claimBusy reports whether a promotion Job that has not ended uses the
// outbox claim, whether the pass listed that Job or created it.
func (w world) claimBusy(namespace, claim string) bool {
	if w.created[namespace+"/"+claim] {
		return true
	}
	return slices.ContainsFunc(w.jobs, func(j jobObject) bool {
		_, promotes := promotionNumber(j)
		ended, _ := finished(j)
		return promotes && !ended && j.Metadata.Namespace == namespace && j.Metadata.Annotations[annotationClaim] == claim
	})
}

// unschedulable returns the scheduler's message for a Job's pod that
// cannot be scheduled, for example when no node can mount both the
// outbox claim and the archive claim, and true when there is such a pod.
func unschedulable(j jobObject, pods []podObject) (string, bool) {
	for _, pod := range pods {
		if pod.Metadata.Labels[labelPromotionJob] != j.Metadata.Name {
			continue
		}
		if c, ok := condition(pod.Status.Conditions, "PodScheduled", "False"); ok && c.Reason == "Unschedulable" {
			return fmt.Sprintf("The pod of the promotion Job %s cannot start: %s", j.Metadata.Name, c.Message), true
		}
	}
	return "", false
}

// startJob creates the Target's next Job for the first outbox claim
// that has frames to promote and no other Job, when the Target's
// promotion setting allows it and the archive claim exists.
func (p *promotions) startJob(t *astrophotography.Target, w world, number int32) error {
	namespace := t.Metadata.Namespace
	for _, entry := range t.Status.Outboxes {
		if entry.Pending <= entry.Left || waitsForReservation(t, entry.ClaimName, w.reservations) || w.claimBusy(namespace, entry.ClaimName) {
			continue
		}
		if _, exists := w.claims[namespace+"/"+t.Spec.Archive.ClaimName]; !exists {
			message := fmt.Sprintf("The PersistentVolumeClaim %s that spec.archive.claimName refers to does not exist in namespace %s", t.Spec.Archive.ClaimName, namespace)
			return p.setCondition(t, archiveAvailable(conditions.False, astrophotography.ReasonClaimMissing, message))
		}
		image, err := p.ownImage()
		if err != nil {
			return err
		}
		job := promotionJob(t, entry.ClaimName, number, entry.Pending, image, w.cameras[namespace])
		if err := create(p.client, p.jobs, "/apis/batch/v1/namespaces/"+namespace+"/jobs", &job); err != nil {
			return err
		}
		w.created[namespace+"/"+entry.ClaimName] = true
		fmt.Fprintf(p.logs, "%s: started the promotion Job %s/%s for %s in %s\n", managedBy, namespace, job.Metadata.Name, frames(entry.Pending), entry.ClaimName)
		return nil
	}
	return nil
}

// waitsForReservation reports whether a Target whose promotion is
// AfterReservation must wait, because a Reservation that uses the outbox
// claim has not ended and its runner can still add frames.
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

func (p *promotions) setCondition(t *astrophotography.Target, next conditions.Condition) error {
	_, err := p.settle(t, func(current *astrophotography.Target) ([]conditions.Condition, bool) {
		before, _ := conditions.Find(current.Status.Conditions, next.Type)
		changed := setCondition(current, nil, next)
		after, _ := conditions.Find(current.Status.Conditions, next.Type)
		return changed, before != after
	})
	return err
}

// settle writes the status that apply returns, and after the write
// succeeds, posts an Event for each condition that transitioned. It
// returns whether the write succeeded.
func (p *promotions) settle(t *astrophotography.Target, apply func(*astrophotography.Target) ([]conditions.Condition, bool)) (bool, error) {
	var changed []conditions.Condition
	path := targetPath(t.Metadata.Namespace, t.Metadata.Name)
	t.APIVersion, t.Kind = astrophotography.APIVersion, astrophotography.TargetKind.Name
	written, err := informer.SettleStatus[astrophotography.Target](p.client, p.versions, path, t, func(current *astrophotography.Target) bool {
		var needed bool
		changed, needed = apply(current)
		return needed
	})
	if written {
		reference := events.ObjectReference{APIVersion: astrophotography.APIVersion, Kind: astrophotography.TargetKind.Name,
			Namespace: t.Metadata.Namespace, Name: t.Metadata.Name, UID: t.Metadata.UID}
		for _, c := range changed {
			p.recorder.Transition(reference, c, badStatus(c))
		}
	}
	return written, err
}

// badStatus returns the status of the condition that needs a person, so
// a transition to it posts a Warning Event. That is False for
// ArchiveAvailable, and none for Complete.
func badStatus(c conditions.Condition) conditions.Status {
	if c.Type == astrophotography.ConditionArchiveAvailable {
		return conditions.False
	}
	return ""
}
