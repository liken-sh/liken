package main

// The batch objects this operator writes and the requests it
// makes for them, hand-written in the same form as the core objects in
// objects.go and reached through the same client. Every worker of a
// namespace is a Job: a scan runs on a schedule from a CronJob, a
// folder scan runs once from a Job the webhook creates, and a departure
// runs once from a Job of its own.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// The group the Job and the CronJob belong to.
const batchAPIVersion = "batch/v1"

// The label Kubernetes stamps on every pod a Job creates, whose
// value is the Job's own name; the scanner reads it through the
// downward API and writes it into the runs row.
const jobNameLabel = "batch.kubernetes.io/job-name"

// The field path the downward API reads that label from.
const jobNameFieldPath = "metadata.labels['" + jobNameLabel + "']"

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

// The counts the Job controller keeps: pods running now, pods
// that exited zero, and pods that failed.
//
// JobStatus is what the Job controller reports: the counts of pods in
// each state, and the conditions, which are its verdict on the whole
// Job. The counts alone cannot say whether a Job is over. A Job
// between the pods of its backoff counts no active pod and is not over.
type JobStatus struct {
	Active     int            `json:"active,omitempty"`
	Succeeded  int            `json:"succeeded,omitempty"`
	Failed     int            `json:"failed,omitempty"`
	Conditions []JobCondition `json:"conditions,omitempty"`
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
	for _, condition := range j.Status.Conditions {
		if condition.Status != ConditionTrue {
			continue
		}
		if condition.Type == jobComplete || condition.Type == jobFailed {
			return true
		}
	}
	return false
}

// The pod one Job or CronJob creates, as the metadata and spec
// the controller stamps onto it.
type PodTemplateSpec struct {
	Metadata ObjectMeta `json:"metadata"`
	Spec     PodSpec    `json:"spec"`
}

// The schedule one Library's full walk runs on. The operator
// writes it and reads nothing back but the resourceVersion, because the
// Jobs it creates are read through the Job list.
type CronJob struct {
	APIVersion string      `json:"apiVersion,omitempty"`
	Kind       string      `json:"kind,omitempty"`
	Metadata   ObjectMeta  `json:"metadata"`
	Spec       CronJobSpec `json:"spec"`
}

// ConcurrencyPolicy is Forbid, so a walk that runs past its next
// turn skips that turn rather than starting a second walk on a claim
// that admits one writer.
type CronJobSpec struct {
	Schedule                   string          `json:"schedule"`
	ConcurrencyPolicy          string          `json:"concurrencyPolicy,omitempty"`
	SuccessfulJobsHistoryLimit *int32          `json:"successfulJobsHistoryLimit,omitempty"`
	FailedJobsHistoryLimit     *int32          `json:"failedJobsHistoryLimit,omitempty"`
	JobTemplate                JobTemplateSpec `json:"jobTemplate"`
}

// The Job one turn of the schedule creates.
type JobTemplateSpec struct {
	Metadata ObjectMeta `json:"metadata"`
	Spec     JobSpec    `json:"spec"`
}

// The batch collections: the Jobs of every namespace read with one
// request, and the Jobs and CronJobs of one namespace written per
// namespace.
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

func GetCronJob(ctx context.Context, c *Client, namespace, name string) (*CronJob, error) {
	cronJob := &CronJob{}
	path := cronJobsPath(namespace) + "/" + name
	if err := c.RequestJSON(ctx, http.MethodGet, path, nil, cronJob); err != nil {
		return nil, err
	}
	return cronJob, nil
}

func CreateCronJob(ctx context.Context, c *Client, cronJob *CronJob) (*CronJob, error) {
	body, err := json.Marshal(cronJob)
	if err != nil {
		return nil, err
	}
	created := &CronJob{}
	path := cronJobsPath(cronJob.Metadata.Namespace)
	if err := c.RequestJSON(ctx, http.MethodPost, path, body, created); err != nil {
		return nil, err
	}
	return created, nil
}

// UpdateCronJob writes the whole CronJob back. The resourceVersion in
// the body makes the write conditional, so a CronJob that changed
// underneath answers ErrConflict and the next pass reads it again.
func UpdateCronJob(ctx context.Context, c *Client, cronJob *CronJob) (*CronJob, error) {
	body, err := json.Marshal(cronJob)
	if err != nil {
		return nil, err
	}
	written := &CronJob{}
	path := cronJobsPath(cronJob.Metadata.Namespace) + "/" + cronJob.Metadata.Name
	if err := c.RequestJSON(ctx, http.MethodPut, path, body, written); err != nil {
		return nil, err
	}
	return written, nil
}

// DeleteCronJob removes one Library's schedule. An already-absent
// CronJob is success, the rule DeleteJob follows.
func DeleteCronJob(ctx context.Context, c *Client, namespace, name string) error {
	path := cronJobsPath(namespace) + "/" + name + backgroundDeletion
	err := c.RequestJSON(ctx, http.MethodDelete, path, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
