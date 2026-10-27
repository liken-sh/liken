package main

// factworkerjob.go is the operator's side of the heavy facts: when it starts
// a fact's worker Job for a Library, and the Job it starts.
//
// The close container of each library Job publishes the fact's gap on the
// bus as a work list, one retained message per video and then the count
// (worklist.go), and then writes its finished enrich run. The operator starts
// a worker when the Library's report holds that finished run, the bus holds a
// count above zero for the same Job, and no worker of the fact runs for the
// Library. The worker is an Indexed Job with one completion per video, so the
// Job controller is the queue: it starts the next index whenever a pod ends,
// up to the Library's parallelism, and each pod goes through the scheduler on
// its own. The operator records which list it started a worker on, so one
// list starts one worker. A worker that is still running when a newer list
// lands finishes its own list, and the pass that sees it end starts a worker
// on the newer one.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// How long a worker Job may run before Kubernetes ends it. A worker holds no
// gate, so the deadline bounds only a worker that hangs. A list after a
// requested re-render can hold days of decoding, so a healthy worker can
// reach the deadline too. Then the Job fails, the videos whose indexes did
// not run stay in the gap, and the next library Job lists them again.
const factWorkerDeadline = 24 * time.Hour

// The annotation a worker Job carries with the library Job whose list it
// works, so a person reads which walk the work came from, and a restarted
// operator reads which list already started a worker.
const workListAnnotation = "library.liken.sh/work-list"

// The completion mode whose pods each read their index from the Job.
const indexedCompletion = "Indexed"

// Whether the pass starts a worker of one fact for one Library on the list
// of the enrich run given, which holds count videos. taken is the library Job
// whose list started the last worker, and empty where this process has
// started none.
//
// A Library that has not turned the fact on, a run that has not finished,
// and a list the bus does not hold start nothing. A list that started a
// worker starts nothing more, so a worker that finished its list does not
// start again on the same list. The worker Jobs the pass listed name their
// lists as well, so a restarted operator reads which list started a worker
// for as long as that Job stays. An unfinished worker of the fact starts
// nothing, so one Library runs one worker of a fact at a time, and two
// workers never decode one video.
func factWorkerDue(library *Library, worker factWorker, listed libraryRun, count int, jobs []Job,
	taken string) bool {
	if !worker.enabled(library) || listed.Finished.IsZero() || listed.Job == taken || count <= 0 {
		return false
	}
	workers := jobsOf(jobs, library.Metadata.Namespace, library.Metadata.Name, worker.fact)
	return !slices.ContainsFunc(workers, func(job Job) bool {
		return !job.finished() || job.Metadata.Annotations[workListAnnotation] == listed.Job
	})
}

// The enrich run of the Library's last library Job, and the zero run where
// the report holds none.
func listedRun(report *libraryReport) libraryRun {
	if report == nil {
		return libraryRun{}
	}
	run, _ := runOf(report.Runs, workerEnrich)
	return run
}

// The worker step of one Library's pass. It starts at most one worker per
// heavy fact. A worker whose GPU claim template does not exist starts no Job,
// because its pods would stay Pending until the Job's deadline. The template's
// create wakes a pass, and that pass starts the worker. Before it decides,
// the step applies the Library's block to the worker of the fact that runs.
func (o *operator) runFactWorkers(ctx context.Context, library *Library, report *libraryReport, jobs []Job,
	templates gpuClaimTemplates, now time.Time) error {
	namespace, name := library.Metadata.Namespace, library.Metadata.Name
	listed := listedRun(report)
	for _, worker := range factWorkers {
		if err := o.editRunningWorkers(ctx, library, worker, jobs); err != nil {
			return err
		}
		if templates.missing(worker.fact) {
			continue
		}
		key := libraryKey(namespace, name) + "/" + worker.fact
		list := workList{namespace: namespace, library: name, fact: worker.fact, run: listed.Job}
		count, _ := o.workLists.countOf(list)
		if !factWorkerDue(library, worker, listed, count, jobs, o.workListsTaken[key]) {
			continue
		}
		job := buildFactWorkerJob(library, worker, o.jobImages(), o.jobBus(),
			webhookURL(o.namespace, namespace, name), listed, count, now)
		_, err := o.createJob(ctx, job)
		if err != nil && !errors.Is(err, apiclient.ErrConflict) {
			return fmt.Errorf("creating the %s worker %s: %w", worker.fact, job.Metadata.Name, err)
		}
		o.workListsTaken[key] = listed.Job
		if err == nil {
			o.logf("library %s/%s: created the job %s to work the %s list of %s, %d at once, from the job %s",
				namespace, name, job.Metadata.Name, worker.fact, counted(count, "video"),
				*job.Spec.Parallelism, listed.Job)
		}
	}
	return nil
}

// editRunningWorkers applies the Library's block for the fact to each worker
// of the fact that runs, so an edit of the Library changes the list that runs
// and does not wait for the next list. A finished worker starts no pod, so it
// stays as it ran.
//
// A Library that turned the fact off deletes the worker, and the pods with
// it, so turning a fact off stops a worker that runs away. The videos it did
// not reach stay in the gap, and the list after the fact is turned on again
// holds them. Otherwise the worker takes the Library's parallelism, which is
// the one field of the block that a running Job can take. The pod template of
// a Job cannot change, so a new GPU claim template reaches only the next
// worker.
func (o *operator) editRunningWorkers(ctx context.Context, library *Library, worker factWorker, jobs []Job) error {
	namespace, name := library.Metadata.Namespace, library.Metadata.Name
	for _, job := range jobsOf(jobs, namespace, name, worker.fact) {
		if job.finished() {
			continue
		}
		if !worker.enabled(library) {
			if err := o.deleteJob(ctx, namespace, job.Metadata.Name); err != nil {
				return fmt.Errorf("deleting the %s worker %s: %w", worker.fact, job.Metadata.Name, err)
			}
			o.logf("library %s/%s: deleted the %s worker %s, because the Library turned %s off",
				namespace, name, worker.fact, job.Metadata.Name, worker.fact)
			continue
		}
		if err := o.resizeWorker(ctx, library, worker, &job); err != nil {
			return err
		}
	}
	return nil
}

// resizeWorker sets one running worker to the parallelism the Library asks
// for, and sends nothing where the worker runs at it already.
func (o *operator) resizeWorker(ctx context.Context, library *Library, worker factWorker, job *Job) error {
	if job.Spec.Parallelism == nil || job.Spec.Completions == nil {
		return nil
	}
	running := *job.Spec.Parallelism
	asked := workerParallelism(library, worker, int(*job.Spec.Completions))
	if running == asked {
		return nil
	}
	err := o.patchJobParallelism(ctx, job, asked)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("changing the parallelism of the %s worker %s: %w", worker.fact, job.Metadata.Name, err)
	}
	o.logf("library %s/%s: changed the %s worker %s from %d to %d videos at once, because the Library asks for %d",
		job.Metadata.Namespace, library.Metadata.Name, worker.fact, job.Metadata.Name, running, asked,
		worker.parallelism(library))
	return nil
}

// How many videos at once a worker of the fact runs on a list of count
// videos: the Library's parallelism, and never more than the list, so no pod
// starts with nothing to do.
func workerParallelism(library *Library, worker factWorker, count int) int32 {
	return int32(min(worker.parallelism(library), count))
}

// The images every Job of this operator runs.
func (o *operator) jobImages() jobImages {
	return jobImages{operator: o.scannerImage, ffmpeg: o.ffmpegImage, corrosion: o.corrosionImage,
		appearances: o.appearancesImage}
}

// The broker every Job of this operator that reads or writes a work list
// reaches.
func (o *operator) jobBus() busEndpoint {
	return busEndpoint{address: o.busAddress, base: o.topicBase}
}

// The worker Job of one fact, owned by the Library so the garbage collector
// takes it with the Library. Its name has the shape of a library Job's, with
// the fact where the mode goes.
//
// It is an Indexed Job of one completion per video of the list. Each index
// has the backoff a library Job has, and the Job states no backoff of its
// own, because a limit for the whole Job counts the failures of every index
// together and would end the indexes that still work. An index that runs out
// of retries leaves the others to finish, and the Job then ends Failed. The
// deadline stays the Job's, so every pod stops at it.
func buildFactWorkerJob(library *Library, worker factWorker, images jobImages, bus busEndpoint, webhook string,
	listed libraryRun, count int, created time.Time) *Job {
	backoff, ttl := int32(scanBackoffLimit), int32(scanJobTTL)
	deadline := int64(factWorkerDeadline / time.Second)
	completions := int32(count)
	parallelism := workerParallelism(library, worker, count)
	return &Job{
		APIVersion: batchAPIVersion,
		Kind:       "Job",
		Metadata: ObjectMeta{
			Name:      libraryJobName(library.Metadata.Name, worker.fact, created),
			Namespace: library.Metadata.Namespace,
			Labels:    workerLabels(library.Metadata.Name, worker.fact),
			Annotations: map[string]string{
				jobCreatedAnnotation: created.UTC().Format(time.RFC3339Nano),
				workListAnnotation:   listed.Job,
			},
			OwnerReferences: []OwnerReference{libraryOwner(library)},
		},
		Spec: JobSpec{
			BackoffLimitPerIndex:    &backoff,
			CompletionMode:          indexedCompletion,
			Completions:             &completions,
			Parallelism:             &parallelism,
			ActiveDeadlineSeconds:   &deadline,
			TTLSecondsAfterFinished: &ttl,
			Template:                factWorkerPod(library, worker, images, bus, webhook, listed),
		},
	}
}

// The worker's pod: the worker's container on the volume the phases write,
// and the device claim the fact takes. The pod holds no catalog and no
// Kubernetes credential. It reads its one video from the bus, and asks the
// operator to rescan the video's folder, which reads the work into the
// catalog.
//
// The spread is ScheduleAnyway. The scheduler counts a node with no matching
// GPU as a place to spread to, and the DRA filter is separate. With
// DoNotSchedule, a cluster with fewer GPU nodes than running pods would hold
// the extra pods Pending until another pod ends. With ScheduleAnyway, the
// claim decides which nodes can take a pod, and the spread only ranks them,
// so an extra pod shares a GPU, which `liken` publishes for many claims at
// once.
func factWorkerPod(library *Library, worker factWorker, images jobImages, bus busEndpoint, webhook string,
	listed libraryRun) PodTemplateSpec {
	grace := int64(scannerGracePeriod)
	noToken := false
	env := []EnvVar{
		{Name: libraryNamespaceVariable, Value: library.Metadata.Namespace},
		{Name: libraryNameVariable, Value: library.Metadata.Name},
		{Name: libraryKindVariable, Value: library.Spec.Kind},
		{Name: libraryRootVariable, Value: library.Spec.Storage.Root},
		{Name: libraryFactVariable, Value: worker.fact},
		{Name: libraryWebhookVariable, Value: webhook},
		{Name: workListVariable, Value: listed.Job},
		{Name: gapSinceVariable, Value: listed.Finished.UTC().Format(time.RFC3339Nano)},
		{Name: jobNameVariable, ValueFrom: &EnvVarSource{
			FieldRef: &ObjectFieldSelector{FieldPath: jobNameFieldPath},
		}},
	}
	container := Container{
		Name:            worker.fact,
		Image:           worker.image(images),
		Command:         []string{podBinary, workerMode},
		Env:             append(env, bus.env()...),
		VolumeMounts:    []VolumeMount{{Name: phaseVolumeOf(library), MountPath: libraryMountPath}},
		Resources:       worker.resources(),
		SecurityContext: unprivileged(),
	}
	labels := workerLabels(library.Metadata.Name, worker.fact)
	spec := PodSpec{
		RestartPolicy:                 "Never",
		TerminationGracePeriodSeconds: &grace,
		AutomountServiceAccountToken:  &noToken,
		Volumes:                       []Volume{factWorkerVolume(library)},
		TopologySpreadConstraints: []TopologySpreadConstraint{{
			MaxSkew:           1,
			TopologyKey:       hostnameTopologyKey,
			WhenUnsatisfiable: "ScheduleAnyway",
			LabelSelector:     &LabelSelector{MatchLabels: maps.Clone(labels)},
		}},
	}
	if claim := gpuClaim(library, worker); claim != nil {
		spec.ResourceClaims = []PodResourceClaim{*claim}
		container.Resources.Claims = []ResourceClaim{{Name: claim.Name}}
	}
	if worker.scratch != "" {
		spec.Volumes = append(spec.Volumes, Volume{Name: scratchVolumeName, EmptyDir: &EmptyDirVolumeSource{}})
		container.VolumeMounts = append(container.VolumeMounts,
			VolumeMount{Name: scratchVolumeName, MountPath: worker.scratch})
	}
	spec.Containers = []Container{container}
	return PodTemplateSpec{Metadata: ObjectMeta{Labels: labels}, Spec: spec}
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
