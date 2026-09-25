package main

// The batch objects this operator writes and the requests it
// makes for them, hand-written in the same form as the core objects in
// objects.go and reached through the same client. Every worker of a
// namespace is a Job: a Library's walks and the phases that fill its
// gaps run in the Job the operator creates for it, and a departure runs
// once from a Job of its own.
//
// the Jellyfin backfill is a worker too, and it belongs to a Catalog rather
// than to a Library.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"
)

// The group Jobs and CronJobs belong to.
const batchAPIVersion = "batch/v1"

// The label Kubernetes stamps on every pod a Job creates, whose
// value is the Job's own name; every container of a library Job reads
// it through the downward API and writes it into the runs row.
const jobNameLabel = "batch.kubernetes.io/job-name"

// The field path the downward API reads that label from.
const jobNameFieldPath = "metadata.labels['" + jobNameLabel + "']"

// The pod's own name, through the downward API. A confirmer keys
// its row by it, because the row says which copy holds the versions.
const podNameFieldPath = "metadata.name"

// One run of one worker. The operator writes the spec and reads
// the status, which is the count of pods in each state.
type Job struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
	Spec       JobSpec    `json:"spec"`
	Status     JobStatus  `json:"status"`
}

// The collection ListWorkerJobs answers.
type JobList struct {
	Metadata ListMeta `json:"metadata"`
	Items    []Job    `json:"items"`
}

// BackoffLimit is how many times Kubernetes replaces a failed
// pod before the Job itself fails, and TTLSecondsAfterFinished is how
// long a finished Job stays for a person to read its logs.
type JobSpec struct {
	BackoffLimit            *int32          `json:"backoffLimit,omitempty"`
	TTLSecondsAfterFinished *int32          `json:"ttlSecondsAfterFinished,omitempty"`
	Template                PodTemplateSpec `json:"template"`
}

// JobStatus is what the Job controller reports: the counts of pods in each
// state, the time it ended the Job, and the conditions, which are its verdict
// on the whole Job. The counts alone cannot say whether a Job is over. A Job
// between the pods of its backoff counts no active pod and is not over.
//
// The completion time is the controller's own stamp on a Job whose pod exited
// zero. The early delete below measures its grace from that stamp, not from
// the pod's exit, so the two clocks are the controller's.
type JobStatus struct {
	Active         int            `json:"active,omitempty"`
	Succeeded      int            `json:"succeeded,omitempty"`
	Failed         int            `json:"failed,omitempty"`
	CompletionTime time.Time      `json:"completionTime,omitzero"`
	Conditions     []JobCondition `json:"conditions,omitempty"`
}

// JobCondition is one verdict of the Job controller, in the shape
// batch/v1 writes it.
type JobCondition struct {
	Type   string          `json:"type"`
	Status ConditionStatus `json:"status"`
	Reason string          `json:"reason,omitempty"`
}

// The two condition types that end a Job, one for each way it ends.
const (
	jobComplete = "Complete"
	jobFailed   = "Failed"
)

// A Job is still doing its work while it has a pod running.
func (j *Job) active() bool { return j.Status.Active > 0 }

// finished is true when the controller has ended the Job, and not
// before. A Job that waits out its backoff with no pod is unfinished.
func (j *Job) finished() bool {
	return j.holds(jobComplete) || j.holds(jobFailed)
}

// A Job with no pod running and a failed pod behind it has given
// up, because Kubernetes replaced that pod up to the backoff limit
// before it stopped.
func (j *Job) gaveUp() bool {
	return !j.active() && j.Status.Succeeded == 0 && j.Status.Failed > 0
}

// succeeded is true when the controller ended the Job on a pod that exited
// zero. That is the one outcome the operator deletes early. A failed Job
// stays for its TTL, because a person reads its logs.
func (j *Job) succeeded() bool { return j.holds(jobComplete) }

// failed is true when the controller ended the Job on the backoff limit.
func (j *Job) failed() bool { return j.holds(jobFailed) }

// holds is true when the Job carries one condition of the given type with
// status True. That is how batch/v1 writes a verdict it stands behind. A
// condition with status False or Unknown is not a verdict.
func (j *Job) holds(conditionType string) bool {
	for _, condition := range j.Status.Conditions {
		if condition.Type == conditionType && condition.Status == ConditionTrue {
			return true
		}
	}
	return false
}

// The pod one Job creates, as the metadata and spec
// the controller stamps onto it.
type PodTemplateSpec struct {
	Metadata ObjectMeta `json:"metadata"`
	Spec     PodSpec    `json:"spec"`
}

// The batch collections: the Jobs of every namespace read with one
// request, and the Jobs of one namespace written per namespace. The
// CronJobs path is there for the delete of the CronJob an earlier
// release stood for each Library.
const (
	jobsAllPath = "/apis/" + batchAPIVersion + "/jobs"
	batchPrefix = "/apis/" + batchAPIVersion + "/namespaces/"
)

func jobsPath(namespace string) string {
	return batchPrefix + namespace + "/jobs"
}

func cronJobsPath(namespace string) string {
	return batchPrefix + namespace + "/cronjobs"
}

// The narrowing that keeps a Job list to this operator's own
// workers, by the name label every Job it creates carries. The equals
// sign is percent-encoded, so the server reads one parameter.
const workerJobsQuery = "labelSelector=" + scannerLabelKey + "%3D" + workerLabelValue

// A delete of a Job removes the pods under it as well, which the
// default policy of orphaning would leave behind holding the claim.
const backgroundDeletion = "?propagationPolicy=Background"

// ListWorkerJobs reads this operator's Jobs across every namespace,
// because a Library is in whatever namespace its claim is.
func ListWorkerJobs(ctx context.Context, c *Client) (*JobList, error) {
	list := &JobList{}
	if err := c.RequestJSON(ctx, http.MethodGet, jobsAllPath+"?"+workerJobsQuery, nil, list); err != nil {
		return nil, err
	}
	return list, nil
}

func CreateJob(ctx context.Context, c *Client, job *Job) (*Job, error) {
	body, err := json.Marshal(job)
	if err != nil {
		return nil, err
	}
	created := &Job{}
	if err := c.RequestJSON(ctx, http.MethodPost, jobsPath(job.Metadata.Namespace), body, created); err != nil {
		return nil, err
	}
	return created, nil
}

// DeleteJob removes one Job and the pods under it. An already-absent
// Job is success, the rule DeletePod follows.
func DeleteJob(ctx context.Context, c *Client, namespace, name string) error {
	path := jobsPath(namespace) + "/" + name + backgroundDeletion
	err := c.RequestJSON(ctx, http.MethodDelete, path, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// How long a succeeded worker Job and its pod stay before the pass deletes
// them. The Job's own TTL is the hour a failed Job keeps for a person to
// read, and it is the backstop when the operator is down.
const succeededJobGrace = 5 * time.Minute

// retireSucceededJobs deletes every worker Job that exited zero longer than
// the grace ago. The pass acts on the list it already read, so a Job it
// deletes still decides this pass and is gone from the next one. A delete
// that fails is reported and the pass carries on, because the next pass
// reads the Job again.
func (o *operator) retireSucceededJobs(ctx context.Context, jobs []Job, now time.Time) {
	cutoff := now.Add(-succeededJobGrace)
	for index := range jobs {
		job := &jobs[index]
		if !job.succeeded() || job.Status.CompletionTime.IsZero() ||
			!job.Status.CompletionTime.Before(cutoff) {
			continue
		}
		if err := DeleteJob(ctx, o.client, job.Metadata.Namespace, job.Metadata.Name); err != nil {
			fmt.Fprintf(os.Stderr, "retiring the finished job %s/%s: %v\n",
				job.Metadata.Namespace, job.Metadata.Name, err)
		}
	}
}

// The Job that follows a failed one waits on the same backoff curve a
// cleanup Job uses, so a cause nobody has repaired costs one Job per delay.
// Without the curve it would cost one Job per pass. The first Job is
// immediate, and the wait after it grows to the cap, which is the shape
// mayStandCleanup has.
func (o *operator) mayRestandFailed(key string, now time.Time) bool {
	state := o.failedStands[key]
	if now.Before(state.next) {
		return false
	}
	state.count++
	state.next = now.Add(cleanupBackoffDelay(state.count))
	o.failedStands[key] = state
	return true
}

// DeleteCronJob removes the CronJob an earlier release ran a Library's
// walk from. An already-absent CronJob is success, the rule DeleteJob
// follows.
func DeleteCronJob(ctx context.Context, c *Client, namespace, name string) error {
	path := cronJobsPath(namespace) + "/" + name + backgroundDeletion
	err := c.RequestJSON(ctx, http.MethodDelete, path, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
