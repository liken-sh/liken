package main

// factworkerjob.go is the operator's side of the heavy facts: when it starts
// a fact's worker Job for a Library, and the Job it starts.
//
// The library Job writes each work list just before its finished runs row,
// so a finished enrich run in the Library's report is the event that says a
// new list is on the volume. The operator starts a worker for the list of
// that run when the report counts the fact's gap above zero and no worker of
// the fact runs for the Library. It records which run's list it gave a
// worker, so a list is worked once and the next one waits for the next
// library Job. A worker that is still running when a newer list lands
// finishes its own list, and the pass that sees it end starts a worker on the
// newer one.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// How long a worker Job may run before Kubernetes ends it. A worker holds no
// gate, so the deadline bounds only a worker that hangs. A list after a
// requested re-render can hold days of decoding, so a healthy worker can
// reach the deadline too. Then the Job fails, the title it was decoding stays
// in the gap, and the next library Job's list starts another worker, which
// passes over every title the ledger has answered since.
const factWorkerDeadline = 24 * time.Hour

// The annotation a worker Job carries with the library Job whose list it
// works, so a person reads which walk the work came from.
const workListAnnotation = "library.liken.sh/work-list"

// Whether the pass starts a worker of one fact for one Library, and the
// library Job whose list the worker takes. taken is the library Job whose
// list the last worker took, and empty where this process has started none.
//
// A Library that has not turned the fact on, a report with no finished enrich
// run, and a gap of zero start nothing. A list a worker took starts nothing,
// so a worker that finished its list does not start again on it. The worker
// Jobs the pass listed name their lists as well, so a restarted operator
// reads which list a worker took for as long as that Job stays. An
// unfinished worker of the fact starts nothing, so one Library runs one
// worker of a fact at a time, and two workers never decode one title.
func factWorkerDue(library *Library, worker factWorker, report *libraryReport, jobs []Job,
	taken string) (string, bool) {
	if !worker.enabled(library) || report == nil {
		return "", false
	}
	listed, ran := runOf(report.Runs, workerEnrich)
	if !ran || listed.Finished.IsZero() || listed.Job == taken || report.Gaps[worker.fact] <= 0 {
		return "", false
	}
	workers := jobsOf(jobs, library.Metadata.Namespace, library.Metadata.Name, worker.fact)
	if slices.ContainsFunc(workers, func(job Job) bool {
		return !job.finished() || job.Metadata.Annotations[workListAnnotation] == listed.Job
	}) {
		return "", false
	}
	return listed.Job, true
}

// The worker step of one Library's pass. It starts at most one worker per
// heavy fact.
func (o *operator) runFactWorkers(ctx context.Context, library *Library, report *libraryReport,
	jobs []Job, now time.Time) error {
	namespace, name := library.Metadata.Namespace, library.Metadata.Name
	for _, worker := range factWorkers {
		key := libraryKey(namespace, name) + "/" + worker.fact
		listed, due := factWorkerDue(library, worker, report, jobs, o.workListsTaken[key])
		if !due {
			continue
		}
		job := buildFactWorkerJob(library, worker, o.jobImages(), webhookURL(o.namespace, namespace, name),
			listed, now)
		_, err := o.createJob(ctx, job)
		if err != nil && !errors.Is(err, apiclient.ErrConflict) {
			return fmt.Errorf("creating the %s worker %s: %w", worker.fact, job.Metadata.Name, err)
		}
		o.workListsTaken[key] = listed
		if err == nil {
			o.logf("library %s/%s: created the job %s to work the %s gap of %s from the list of the job %s",
				namespace, name, job.Metadata.Name, worker.fact, counted(report.Gaps[worker.fact], "video"), listed)
		}
	}
	return nil
}

// The images every Job of this operator runs.
func (o *operator) jobImages() jobImages {
	return jobImages{operator: o.scannerImage, ffmpeg: o.ffmpegImage, corrosion: o.corrosionImage,
		appearances: o.appearancesImage}
}

// The worker Job of one fact, owned by the Library so the garbage collector
// takes it with the Library. Its name has the shape of a library Job's, with
// the fact where the mode goes.
func buildFactWorkerJob(library *Library, worker factWorker, images jobImages, webhook, listed string,
	created time.Time) *Job {
	backoff, ttl := int32(scanBackoffLimit), int32(scanJobTTL)
	deadline := int64(factWorkerDeadline / time.Second)
	return &Job{
		APIVersion: batchAPIVersion,
		Kind:       "Job",
		Metadata: ObjectMeta{
			Name:      libraryJobName(library.Metadata.Name, worker.fact, created),
			Namespace: library.Metadata.Namespace,
			Labels:    workerLabels(library.Metadata.Name, worker.fact),
			Annotations: map[string]string{
				jobCreatedAnnotation: created.UTC().Format(time.RFC3339Nano),
				workListAnnotation:   listed,
			},
			OwnerReferences: []OwnerReference{libraryOwner(library)},
		},
		Spec: JobSpec{
			BackoffLimit:            &backoff,
			ActiveDeadlineSeconds:   &deadline,
			TTLSecondsAfterFinished: &ttl,
			Template:                factWorkerPod(library, worker, images, webhook),
		},
	}
}

// The worker's pod: one container on the volume the phases write, and the
// device claim the fact takes. It carries no member label, because it holds
// no catalog agent, and no Kubernetes credential, because it reads nothing
// from the API server.
func factWorkerPod(library *Library, worker factWorker, images jobImages, webhook string) PodTemplateSpec {
	grace := int64(scannerGracePeriod)
	noToken := false
	container := Container{
		Name:    worker.fact,
		Image:   worker.image(images),
		Command: []string{podBinary, workerMode},
		Env: []EnvVar{
			{Name: libraryNamespaceVariable, Value: library.Metadata.Namespace},
			{Name: libraryNameVariable, Value: library.Metadata.Name},
			{Name: libraryKindVariable, Value: library.Spec.Kind},
			{Name: libraryRootVariable, Value: library.Spec.Storage.Root},
			{Name: libraryFactVariable, Value: worker.fact},
			{Name: libraryWebhookVariable, Value: webhook},
			{Name: jobNameVariable, ValueFrom: &EnvVarSource{
				FieldRef: &ObjectFieldSelector{FieldPath: jobNameFieldPath},
			}},
		},
		VolumeMounts:    []VolumeMount{{Name: phaseVolumeOf(library), MountPath: libraryMountPath}},
		Resources:       worker.resources(),
		SecurityContext: unprivileged(),
	}
	spec := PodSpec{
		RestartPolicy:                 "Never",
		TerminationGracePeriodSeconds: &grace,
		AutomountServiceAccountToken:  &noToken,
		Volumes:                       []Volume{factWorkerVolume(library)},
	}
	if claim := renderClaim(library, worker); claim != nil {
		spec.ResourceClaims = []PodResourceClaim{*claim}
		container.Resources.Claims = []ResourceClaim{{Name: claim.Name}}
	}
	if worker.scratch != "" {
		spec.Volumes = append(spec.Volumes, Volume{Name: scratchVolumeName, EmptyDir: &EmptyDirVolumeSource{}})
		container.VolumeMounts = append(container.VolumeMounts,
			VolumeMount{Name: scratchVolumeName, MountPath: worker.scratch})
	}
	spec.Containers = []Container{container}
	return PodTemplateSpec{
		Metadata: ObjectMeta{Labels: workerLabels(library.Metadata.Name, worker.fact)},
		Spec:     spec,
	}
}

// The name of the emptyDir a worker that names a scratch directory mounts.
const scratchVolumeName = "scratch"

// The one volume a worker writes: the claim the library Job's phases write
// beside the media, which is the art claim of a franchises library and the
// storage claim of every other kind.
func factWorkerVolume(library *Library) Volume {
	claim := library.Spec.Storage.Claim
	if art := library.Spec.artClaim(); art != "" {
		claim = art
	}
	return Volume{Name: phaseVolumeOf(library), PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
		ClaimName: claim,
	}}
}
