package main

// factworkerjob.go is the operator's side of the heavy facts: when it starts
// a fact's worker Job for a Library, and the Job it starts.
//
// A finished enrich run in the Library's report is the event that says the
// catalog holds the fact's gap as that library Job left it. The operator
// starts a worker after that run when the report counts the fact's gap above
// zero and no worker of the fact runs for the Library. Each pod of the worker
// waits until its own copy of the catalog holds the run, and then reads the
// gap from that copy. The operator records which run it started a worker
// after, so one run starts one worker, and the next waits for the next
// library Job. A worker that is still running when a newer run finishes
// finishes its own share, and the pass that sees it end starts a worker after
// the newer run.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// How long a worker Job may run before Kubernetes ends it. A worker holds no
// gate, so the deadline bounds only a worker that hangs. A list after a
// requested re-render can hold days of decoding, so a healthy worker can
// reach the deadline too. Then the Job fails, the title it was decoding stays
// in the gap, and the next library Job starts another worker, which passes
// over every title the ledger has answered since.
const factWorkerDeadline = 24 * time.Hour

// The annotation a worker Job carries with the library Job whose enrich run
// the worker reads its gap after, so a person reads which walk the work came
// from, and a restarted operator reads which run already started a worker.
const workListAnnotation = "library.liken.sh/work-list"

// Whether the pass starts a worker of one fact for one Library, and the
// enrich run the worker reads its gap after. taken is the library Job whose
// run started the last worker, and empty where this process has started none.
//
// A Library that has not turned the fact on, a report with no finished enrich
// run, and a gap of zero with no refresh left to work start nothing. A run
// that started a worker starts nothing more, so a worker that finished its
// gap does not start again on the same run. The worker Jobs the pass listed
// name their runs as well, so a restarted operator reads which run started a
// worker for as long as that Job stays. An unfinished worker of the fact
// starts nothing, so one Library runs one worker of a fact at a time, and two
// workers never decode one title.
func factWorkerDue(library *Library, worker factWorker, report *libraryReport, jobs []Job,
	taken string) (libraryRun, bool) {
	if !worker.enabled(library) || report == nil {
		return libraryRun{}, false
	}
	listed, ran := runOf(report.Runs, workerEnrich)
	if !ran || listed.Finished.IsZero() || listed.Job == taken {
		return libraryRun{}, false
	}
	// A report built before the reporter received the Library's refresh
	// times counts the titles a refresh reopens as answered, so the fact's
	// oldest attempt is read beside the count. The worker reads the gap with
	// the refresh time, so it has those titles to work.
	if report.Gaps[worker.fact] <= 0 && !refreshHasWork(library, report, worker.fact) {
		return libraryRun{}, false
	}
	workers := jobsOf(jobs, library.Metadata.Namespace, library.Metadata.Name, worker.fact)
	if slices.ContainsFunc(workers, func(job Job) bool {
		return !job.finished() || job.Metadata.Annotations[workListAnnotation] == listed.Job
	}) {
		return libraryRun{}, false
	}
	return listed, true
}

// The worker step of one Library's pass. It starts at most one worker per
// heavy fact. A worker whose GPU claim template does not exist starts no Job,
// because its pods would stay Pending until the Job's deadline. The template's
// create wakes a pass, and that pass starts the worker. The pass runs only in
// a namespace with one Catalog, whose cluster each worker pod's agent joins.
func (o *operator) runFactWorkers(ctx context.Context, library *Library, catalog *NamespaceCatalog,
	report *libraryReport, jobs []Job, templates gpuClaimTemplates, now time.Time) error {
	namespace, name := library.Metadata.Namespace, library.Metadata.Name
	for _, worker := range factWorkers {
		if templates.missing(worker.fact) {
			continue
		}
		key := libraryKey(namespace, name) + "/" + worker.fact
		listed, due := factWorkerDue(library, worker, report, jobs, o.workListsTaken[key])
		if !due {
			continue
		}
		job := buildFactWorkerJob(library, catalog, worker, o.jobImages(),
			webhookURL(o.namespace, namespace, name), listed, now)
		_, err := o.createJob(ctx, job)
		if err != nil && !errors.Is(err, apiclient.ErrConflict) {
			return fmt.Errorf("creating the %s worker %s: %w", worker.fact, job.Metadata.Name, err)
		}
		o.workListsTaken[key] = listed.Job
		if err == nil {
			o.logf("library %s/%s: created the job %s to work the %s gap of %s%s after the job %s",
				namespace, name, job.Metadata.Name, worker.fact, counted(report.Gaps[worker.fact], "video"),
				inPods(worker.parallelism(library)), listed.Job)
		}
	}
	return nil
}

// The words a creation line adds for a worker of several pods.
func inPods(pods int) string {
	if pods <= 1 {
		return ""
	}
	return " in " + counted(pods, "pod")
}

// The images every Job of this operator runs.
func (o *operator) jobImages() jobImages {
	return jobImages{operator: o.scannerImage, ffmpeg: o.ffmpegImage, corrosion: o.corrosionImage,
		appearances: o.appearancesImage}
}

// The worker Job of one fact, owned by the Library so the garbage collector
// takes it with the Library. Its name has the shape of a library Job's, with
// the fact where the mode goes.
func buildFactWorkerJob(library *Library, catalog *NamespaceCatalog, worker factWorker, images jobImages,
	webhook string, listed libraryRun, created time.Time) *Job {
	backoff, ttl := int32(scanBackoffLimit), int32(scanJobTTL)
	deadline := int64(factWorkerDeadline / time.Second)
	job := &Job{
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
			BackoffLimit:            &backoff,
			ActiveDeadlineSeconds:   &deadline,
			TTLSecondsAfterFinished: &ttl,
			Template:                factWorkerPod(library, catalog, worker, images, webhook, listed),
		},
	}
	if pods := worker.parallelism(library); pods > 1 {
		spreadFactWorker(job, int32(pods))
	}
	return job
}

// The completion mode whose pods each read their index from the Job.
const indexedCompletion = "Indexed"

// A worker Job of several pods, built on the Job of one: an Indexed Job whose
// pods each work one share of the gap (workershare.go).
//
// Each index has the backoff the Job of one pod has, and the Job states no
// backoff of its own, because a limit for the whole Job counts the failures
// of every index together and would end the indexes that still work. An
// index that runs out of retries leaves the others to finish, and the Job
// then ends Failed. The deadline stays the Job's, so every pod stops at it.
//
// The spread is ScheduleAnyway. The scheduler counts a node with no matching
// GPU as a place to spread to, and the DRA filter is separate. With
// DoNotSchedule, a cluster with fewer GPU nodes than pods would hold the
// extra pods Pending until another pod ends. With ScheduleAnyway, the claim
// decides which nodes can take a pod, and the spread only ranks them, so an
// extra pod shares a GPU, which `liken` publishes for many claims at once.
func spreadFactWorker(job *Job, pods int32) {
	backoff := int32(scanBackoffLimit)
	job.Spec.BackoffLimit = nil
	job.Spec.BackoffLimitPerIndex = &backoff
	job.Spec.CompletionMode = indexedCompletion
	job.Spec.Completions = &pods
	job.Spec.Parallelism = &pods
	pod := &job.Spec.Template
	pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env,
		EnvVar{Name: workerParallelismVariable, Value: strconv.Itoa(int(pods))})
	pod.Spec.TopologySpreadConstraints = []TopologySpreadConstraint{{
		MaxSkew:           1,
		TopologyKey:       hostnameTopologyKey,
		WhenUnsatisfiable: "ScheduleAnyway",
		LabelSelector:     &LabelSelector{MatchLabels: maps.Clone(pod.Metadata.Labels)},
	}}
}

// The worker's pod: the worker's container on the volume the phases write,
// the device claim the fact takes, and a catalog agent as a native sidecar,
// the way a library Job's pod runs one. The member label makes the agent a
// peer of the namespace's catalog cluster, so it syncs a copy of its own, and
// the worker reads its gap from that copy on loopback. The pod holds no
// Kubernetes credential, because it reads nothing from the API server.
//
// The worker waits until its copy holds the enrich run it was started after,
// the wait every phase makes before it reads a gap (enrichsync.go). The
// worker writes no catalog row, so the pod makes no hand-off.
func factWorkerPod(library *Library, catalog *NamespaceCatalog, worker factWorker, images jobImages,
	webhook string, listed libraryRun) PodTemplateSpec {
	grace := int64(scannerGracePeriod)
	noToken := false
	pods := worker.parallelism(library)
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
			{Name: catalogAPIVariable, Value: defaultCatalogAPI},
			{Name: libraryRefreshVariable, Value: refreshValue(library)},
			{Name: syncActorVariable, Value: listed.Actor},
			{Name: syncVersionVariable, Value: strconv.FormatInt(listed.Version, 10)},
			{Name: syncTimeoutVariable, Value: defaultSyncTimeout.String()},
			{Name: gapSinceVariable, Value: listed.Finished.UTC().Format(time.RFC3339Nano)},
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
		InitContainers: []Container{
			workerAgent(catalog, library.Metadata.Name, worker.fact, images.corrosion, pods),
		},
		Volumes: []Volume{factWorkerVolume(library), workerCopyVolume(catalog)},
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
	return PodTemplateSpec{
		Metadata: ObjectMeta{Labels: withMemberLabel(workerLabels(library.Metadata.Name, worker.fact))},
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
