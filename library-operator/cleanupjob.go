package main

// The cleanup Job is what a deleting Library becomes on its way
// out: the scan pod with the walk taken off it. It runs the same image
// in its cleanup role, beside the same Corrosion agent on the departing
// library's own catalog claim, and it mounts no media volume, because
// it reads no media. That claim already holds every row of the
// namespace's catalog, so the agent starts with nothing to sync and the
// sweep acts on local rows at once.

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// The Job one departure becomes, named from the Library, so
// every pass names the same Job and the report's echo names it back.
func cleanupJobName(library string) string {
	return library + "-cleanup"
}

// The Job that sweeps one library's rows, built from the Library
// and the operator's own settings alone.
func buildCleanupJob(library *Library, scannerImage, corrosionImage string) *Job {
	backoff, ttl := int32(scanBackoffLimit), int32(scanJobTTL)
	return &Job{
		APIVersion: batchAPIVersion,
		Kind:       "Job",
		Metadata: ObjectMeta{
			Name:            cleanupJobName(library.Metadata.Name),
			Namespace:       library.Metadata.Namespace,
			Labels:          workerLabels(library.Metadata.Name, workerCleanup),
			OwnerReferences: []OwnerReference{libraryOwner(library)},
		},
		Spec: JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: workerPodTemplate(library, workerCleanup,
				cleanupSidecar(library, scannerImage), corrosionImage),
		},
	}
}

// The container that runs the sweep. It learns its library from
// its environment alone, it reads the catalog API at the same loopback
// address the scanner reads, and the Job's own name reaches it through
// the downward API, because it writes that name into the runs row.
func cleanupSidecar(library *Library, image string) Container {
	return Container{
		Name:    cleanupContainer,
		Image:   image,
		Command: []string{"/library-operator", cleanupMode},
		Env: []EnvVar{
			{Name: libraryNamespaceVariable, Value: library.Metadata.Namespace},
			{Name: libraryNameVariable, Value: library.Metadata.Name},
			{Name: catalogAPIVariable, Value: defaultCatalogAPI},
			{Name: jobNameVariable, ValueFrom: &EnvVarSource{
				FieldRef: &ObjectFieldSelector{FieldPath: jobNameFieldPath},
			}},
		},
		Resources: ResourceRequirements{
			Requests: map[string]string{"cpu": scannerCPURequest, "memory": scannerMemoryRequest},
			Limits:   map[string]string{"memory": scannerMemoryLimit},
		},
		SecurityContext: unprivileged(),
	}
}

// The cleanup Job that stands after this pass. There is no
// template hash here, the one departure from every other object this
// operator stands: the Job exists for one teardown, and a rebuild would
// restart the sweep it is running. A Job that failed is deleted on a
// backoff and created again, because the claim admits one holder at a
// time and the failed Job's pod is that holder until it goes.
func (o *operator) standCleanupJob(ctx context.Context, library *Library, jobs []Job) (*Job, error) {
	namespace, name := library.Metadata.Namespace, library.Metadata.Name
	live := cleanupJobOf(jobs, namespace, name)
	if live == nil {
		if !o.mayStandCleanup(libraryKey(namespace, name)) {
			return nil, nil
		}
		created, err := CreateJob(ctx, o.client, buildCleanupJob(library, o.scannerImage, o.corrosionImage))
		if errors.Is(err, ErrConflict) {
			// Another writer created it first, which is the state this
			// create was for; the next pass reads it.
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return created, nil
	}
	if live.Metadata.DeletionTimestamp != "" {
		return live, nil
	}
	if cleanupFailed(live) {
		if err := DeleteJob(ctx, o.client, namespace, cleanupJobName(name)); err != nil {
			return nil, err
		}
	}
	return live, nil
}

// The cleanup Job of one Library out of the Jobs the pass
// listed, or nil when none stands.
func cleanupJobOf(jobs []Job, namespace, library string) *Job {
	held := jobsOf(jobs, namespace, library, workerCleanup)
	if len(held) == 0 {
		return nil
	}
	return &held[0]
}

// A Job with no pod running and a failed pod behind it has given
// up, because Kubernetes replaced that pod up to the backoff limit
// before it stopped.
func cleanupFailed(job *Job) bool {
	return !job.active() && job.Status.Succeeded == 0 && job.Status.Failed > 0
}

// The sentence a person acts on when a cleanup Job will not
// finish, in the cluster's own counts.
func cleanupBlocker(job *Job) string {
	if job == nil || !cleanupFailed(job) {
		return ""
	}
	return fmt.Sprintf("the cleanup job %s failed after %d attempts",
		job.Metadata.Name, job.Status.Failed)
}

// The recreate backoff for a cleanup Job that keeps failing. The
// first stand is immediate, and each later one waits double the last,
// up to the cap, the same shape the kubelet's own crash backoff has.
var (
	cleanupBackoffBase = 10 * time.Second
	cleanupBackoffCap  = 5 * time.Minute
)

// cleanupStand is one departing library's stand count and the
// earliest time it may stand its cleanup Job again.
type cleanupStand struct {
	count int
	next  time.Time
}

// mayStandCleanup never answers no forever: the wait grows to the
// cap and stops there, so the pass keeps trying for as long as the
// Library is deleting.
func (o *operator) mayStandCleanup(key string) bool {
	now := time.Now()
	state := o.cleanupStands[key]
	if now.Before(state.next) {
		return false
	}
	state.count++
	state.next = now.Add(cleanupBackoffDelay(state.count))
	o.cleanupStands[key] = state
	return true
}

// cleanupBackoffDelay is the base doubled once per stand, capped, so
// a long run of failures never overflows the duration.
func cleanupBackoffDelay(count int) time.Duration {
	delay := cleanupBackoffBase
	for range count - 1 {
		delay *= 2
		if delay >= cleanupBackoffCap {
			return cleanupBackoffCap
		}
	}
	return delay
}

// Whether the reporter has echoed this cleanup Job. The Job
// writes its runs row last and the catalog pod publishes the row back
// in the library's report, so a run that names this Job with a time on
// it is the proof that the deletes reached the standing catalog.
func cleanupEchoed(latest *libraryReport, job string) bool {
	if latest == nil {
		return false
	}
	for _, run := range latest.Runs {
		if run.Worker == workerCleanup && run.Job == job && !run.Finished.IsZero() {
			return true
		}
	}
	return false
}

// The departing Library's rows are gone once its cleanup Job
// exited zero and the reporter echoed that same Job.
func cleanupComplete(job *Job, latest *libraryReport) bool {
	return job != nil && job.Status.Succeeded > 0 && cleanupEchoed(latest, job.Metadata.Name)
}

// The cleanup Job of a released Library goes with its pods, so
// nothing is left holding the claim the garbage collector removes next.
func (o *operator) retireCleanupJob(ctx context.Context, namespace, library string) error {
	return DeleteJob(ctx, o.client, namespace, cleanupJobName(library))
}
