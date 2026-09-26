package main

// libraryjobfault.go reads whether the Jobs of a Library do their work. The
// operator runs one Job of a Library at a time, so a Job whose pod cannot
// start holds every other Job of the Library until its deadline. A Library
// whose Job failed does no work until a later Job succeeds. The Ready
// condition names such a Job and the reason Kubernetes gives, so a person
// reads the fault with kubectl get library and does not have to find the pod.

import (
	"context"
	"fmt"
	"os"
	"time"
)

// How long the pod of a Job may stay Pending before the status names the Job.
// A pod pulls its images, mounts its volumes, and waits for the agent's
// startup probe before its containers start. The startup probe allows 90
// seconds, and the first pull of the ffmpeg image onto a small machine takes
// minutes, so a shorter time names healthy Jobs.
const jobStartGrace = 5 * time.Minute

// One fault of a Library's Jobs: the reason the Ready condition takes, the
// Job, the pod that has not started, and the words Kubernetes gives for it.
// The pod is empty for a Job that failed and for a Job that has no pod. The
// cause is empty when Kubernetes gave no reason.
type jobFault struct {
	reason string
	job    string
	pod    string
	cause  string
}

// The sentence the Ready condition carries.
func (f *jobFault) message() string {
	var message string
	switch {
	case f.reason == reasonJobFailed:
		message = "the Job " + f.job + " failed"
	case f.pod != "":
		message = "the pod " + f.pod + " of the Job " + f.job + " has not started"
	default:
		message = "the Job " + f.job + " has no pod"
	}
	if f.cause != "" {
		message += ": " + f.cause
	}
	return message
}

// libraryJobFault reads the Jobs and pods one pass listed, and the runs the
// reporter published, for the fault of one Library's Jobs, or nil. A Job that
// has not started comes first, because it is the Job that holds the gate now.
func libraryJobFault(jobs []Job, pods []Pod, runs []libraryRun, namespace, library string,
	now time.Time) *jobFault {
	held := jobsOfLibrary(jobs, namespace, library)
	for index := range held {
		if fault := notStarted(&held[index], pods, now); fault != nil {
			return fault
		}
	}
	return failedWithNoLaterSuccess(held, runs)
}

// The fault of an unfinished Job that has no pod running and whose pod has
// been Pending for longer than the grace, or nil. A Job whose earlier pod
// failed and whose next pod has not been created yet waits out its backoff,
// which is not a fault. The grace counts from the scheduler's verdict on the
// pod, and from the Job's creation for a Job with no pod.
func notStarted(job *Job, pods []Pod, now time.Time) *jobFault {
	if job.finished() {
		return nil
	}
	var pending *Pod
	count := 0
	for index := range pods {
		pod := &pods[index]
		if pod.Metadata.Namespace != job.Metadata.Namespace || pod.Metadata.Labels[jobNameLabel] != job.Metadata.Name {
			continue
		}
		count++
		switch pod.Status.Phase {
		case podPending:
			pending = pod
		case podRunning, podSucceeded:
			return nil
		}
	}
	since := jobCreated(job)
	if pending != nil {
		if scheduled := podCondition(pending, podScheduled); !scheduled.LastTransitionTime.IsZero() {
			since = scheduled.LastTransitionTime
		}
	} else if count > 0 {
		return nil
	}
	if since.IsZero() || now.Sub(since) <= jobStartGrace {
		return nil
	}
	fault := &jobFault{reason: reasonJobNotStarted, job: job.Metadata.Name}
	if pending != nil {
		fault.pod = pending.Metadata.Name
		if scheduled := podCondition(pending, podScheduled); scheduled.Status == conditionIsFalse {
			fault.cause = kubernetesWords(scheduled.Reason, scheduled.Message)
		}
	}
	return fault
}

// The newest failed Job of one Library, as a fault, unless a later Job
// succeeded.
func failedWithNoLaterSuccess(jobs []Job, runs []libraryRun) *jobFault {
	failed := unansweredFailure(jobs, runs)
	if failed == nil {
		return nil
	}
	fault := &jobFault{reason: reasonJobFailed, job: failed.Metadata.Name}
	for _, condition := range failed.Status.Conditions {
		if condition.Type == jobFailed && condition.Status == ConditionTrue {
			fault.cause = kubernetesWords(condition.Reason, condition.Message)
		}
	}
	return fault
}

// The newest failed Job among the Jobs of one Library, unless a Job created
// after it succeeded, and nil otherwise. The operator deletes a Job that
// succeeded after succeededJobGrace, and a failed Job stays for an hour, so a
// finished run row with no failure that started after the failed Job counts
// as a later success too. The gate's backoff and the Ready condition both
// read this one rule.
func unansweredFailure(jobs []Job, runs []libraryRun) *Job {
	var failed *Job
	for index := range jobs {
		job := &jobs[index]
		if job.failed() && (failed == nil || jobCreated(job).After(jobCreated(failed))) {
			failed = job
		}
	}
	if failed == nil {
		return nil
	}
	at := jobCreated(failed)
	for index := range jobs {
		if jobs[index].succeeded() && jobCreated(&jobs[index]).After(at) {
			return nil
		}
	}
	for _, run := range runs {
		if run.Failure == "" && !run.Finished.IsZero() && run.Started.After(at) {
			return nil
		}
	}
	return failed
}

// One condition of a pod by type, and the zero condition for a type the pod
// does not carry.
func podCondition(pod *Pod, conditionType string) PodCondition {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == conditionType {
			return condition
		}
	}
	return PodCondition{}
}

// A reason word and a sentence in the form kubectl describe prints them,
// with either one left out when it is empty.
func kubernetesWords(reason, message string) string {
	switch {
	case reason == "":
		return message
	case message == "":
		return reason
	default:
		return reason + ": " + message
	}
}

// The fault of this Library's Jobs, with the words of the newest Warning
// event where the pod's status gives no reason. A placed pod whose volume
// the kubelet cannot mount has no reason in its status, and the event is the
// one place the kubelet writes it. The read happens only for a Job that has
// not started, so a healthy pass makes no extra request. A read that fails
// leaves the cause empty, and the status still names the Job.
func (o *operator) observeJobFault(ctx context.Context, jobs []Job, pods []Pod, report *libraryReport,
	namespace, library string, now time.Time) *jobFault {
	fault := libraryJobFault(jobs, pods, reportRuns(report), namespace, library, now)
	if fault == nil || fault.reason != reasonJobNotStarted || fault.cause != "" {
		return fault
	}
	about := fault.pod
	if about == "" {
		about = fault.job
	}
	events, err := ListWarningEvents(ctx, o.client, namespace, about)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the events of %s/%s: %v\n", namespace, about, err)
		return fault
	}
	if newest := newestEvent(events.Items); newest != nil {
		fault.cause = kubernetesWords(newest.Reason, newest.Message)
	}
	return fault
}
