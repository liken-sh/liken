package main

// What these tests read: the deadline every Job of a Library carries, and the
// status of a Library whose Job has not started its pod or has failed.

import (
	"testing"
	"time"
)

// Kubernetes fails a Job at its deadline, so a Job whose pod never starts
// opens the one-Job gate again.
func TestEveryLibraryJobCarriesTheDeadline(t *testing.T) {
	cases := []struct {
		name string
		job  *Job
	}{
		{name: "a walk", job: testEnrichJob(studioMovies(), "")},
		{name: "the cleanup", job: buildCleanupJob(studioMovies(), "scanner", "corrosion")},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			deadline := one.job.Spec.ActiveDeadlineSeconds
			if deadline == nil || *deadline != int64(libraryJobDeadline/time.Second) {
				t.Errorf("activeDeadlineSeconds = %v, want %d", deadline, int64(libraryJobDeadline/time.Second))
			}
		})
	}
}

// a Job of the movies Library that the operator created at the time given.
func moviesJob(name string, created time.Time) Job {
	job := runningJob(name, "house", workerLabels("movies", jobModeWalk))
	job.Metadata.Annotations = map[string]string{jobCreatedAnnotation: created.Format(time.RFC3339Nano)}
	return job
}

// a Job of the movies Library that the controller ended with the condition
// given.
func endedMoviesJob(name string, created time.Time, condition JobCondition) Job {
	job := moviesJob(name, created)
	job.Status = JobStatus{Conditions: []JobCondition{condition}}
	return job
}

// the pod of a Job, in the phase given, that the scheduler placed or refused
// at the time given.
func jobPod(job, phase string, scheduled PodCondition) Pod {
	return Pod{
		Metadata: ObjectMeta{Name: job + "-pod", Namespace: "house",
			Labels: map[string]string{jobNameLabel: job}},
		Status: PodStatus{Phase: phase, Conditions: []PodCondition{scheduled}},
	}
}

// the scheduler's verdict on a pod it placed at the time given.
func placedAt(at time.Time) PodCondition {
	return PodCondition{Type: podScheduled, Status: "True", LastTransitionTime: at}
}

// the scheduler's verdict on a pod no node can take.
func refusedAt(at time.Time) PodCondition {
	return PodCondition{Type: podScheduled, Status: conditionIsFalse, Reason: "Unschedulable",
		Message: "0/2 nodes are available: 2 Insufficient memory.", LastTransitionTime: at}
}

var deadlineExceeded = JobCondition{Type: jobFailed, Status: ConditionTrue,
	Reason: "DeadlineExceeded", Message: "Job was active longer than specified deadline"}

// A Job whose pod has not started for longer than the grace is named with the
// words Kubernetes gives. A Job that failed is named until a later Job
// succeeds.
func TestLibraryJobFaultNamesTheJobAndTheReason(t *testing.T) {
	late := testNow.Add(-jobStartGrace - time.Minute)
	early := testNow.Add(-jobStartGrace + time.Minute)
	earlier := testNow.Add(-time.Hour)

	cases := []struct {
		name  string
		jobs  []Job
		pods  []Pod
		runs  []libraryRun
		fault *jobFault
	}{
		{
			name: "a pod no node can take, past the grace",
			jobs: []Job{moviesJob("movies-walk-2", late)},
			pods: []Pod{jobPod("movies-walk-2", podPending, refusedAt(late))},
			fault: &jobFault{reason: reasonJobNotStarted, job: "movies-walk-2", pod: "movies-walk-2-pod",
				cause: "Unschedulable: 0/2 nodes are available: 2 Insufficient memory."},
		},
		{
			name: "a placed pod whose containers have not started, past the grace",
			jobs: []Job{moviesJob("movies-walk-2", late)},
			pods: []Pod{jobPod("movies-walk-2", podPending, placedAt(late))},
			fault: &jobFault{reason: reasonJobNotStarted, job: "movies-walk-2",
				pod: "movies-walk-2-pod"},
		},
		{
			name:  "a Job with no pod, past the grace",
			jobs:  []Job{moviesJob("movies-walk-2", late)},
			fault: &jobFault{reason: reasonJobNotStarted, job: "movies-walk-2"},
		},
		{
			name: "a pod inside the grace",
			jobs: []Job{moviesJob("movies-walk-2", early)},
			pods: []Pod{jobPod("movies-walk-2", podPending, placedAt(early))},
		},
		{
			name: "a pod that runs",
			jobs: []Job{moviesJob("movies-walk-2", late)},
			pods: []Pod{jobPod("movies-walk-2", podRunning, placedAt(late))},
		},
		{
			name: "a Job between the pods of its backoff",
			jobs: []Job{moviesJob("movies-walk-2", late)},
			pods: []Pod{jobPod("movies-walk-2", podFailed, placedAt(late))},
		},
		{
			name: "a Job that passed its deadline",
			jobs: []Job{endedMoviesJob("movies-walk-2", earlier, deadlineExceeded)},
			fault: &jobFault{reason: reasonJobFailed, job: "movies-walk-2",
				cause: "DeadlineExceeded: Job was active longer than specified deadline"},
		},
		{
			name: "a failed Job and a later Job that runs",
			jobs: []Job{
				endedMoviesJob("movies-walk-2", earlier, deadlineExceeded),
				moviesJob("movies-gaps-3", early),
			},
			pods: []Pod{jobPod("movies-gaps-3", podRunning, placedAt(early))},
			fault: &jobFault{reason: reasonJobFailed, job: "movies-walk-2",
				cause: "DeadlineExceeded: Job was active longer than specified deadline"},
		},
		{
			name: "a failed Job and a later Job that succeeded",
			jobs: []Job{
				endedMoviesJob("movies-walk-2", earlier, deadlineExceeded),
				endedMoviesJob("movies-gaps-3", early, JobCondition{Type: jobComplete, Status: ConditionTrue}),
			},
		},
		{
			name: "a failed Job and the run row of a later Job that the operator deleted",
			jobs: []Job{endedMoviesJob("movies-walk-2", earlier, deadlineExceeded)},
			runs: []libraryRun{{Worker: workerEnrich, Job: "movies-gaps-3", Started: early, Finished: testNow}},
		},
		{
			name: "a failed Job and a run row from before it",
			jobs: []Job{endedMoviesJob("movies-walk-2", earlier, deadlineExceeded)},
			runs: []libraryRun{{Worker: workerEnrich, Job: "movies-gaps-1",
				Started: earlier.Add(-time.Hour), Finished: earlier.Add(-time.Minute)}},
			fault: &jobFault{reason: reasonJobFailed, job: "movies-walk-2",
				cause: "DeadlineExceeded: Job was active longer than specified deadline"},
		},
		{
			name: "a stuck Job of another Library",
			jobs: []Job{runningJob("series-walk-2", "house", workerLabels("series", jobModeWalk))},
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			fault := libraryJobFault(one.jobs, one.pods, one.runs, "house", "movies", testNow)

			if (fault == nil) != (one.fault == nil) || (fault != nil && *fault != *one.fault) {
				t.Errorf("fault = %+v, want %+v", fault, one.fault)
			}
		})
	}
}

// The fault is the Ready condition's reason and message, and the phase says
// it in one word.
func TestDeriveStatusReportsAJobFault(t *testing.T) {
	cases := []struct {
		name    string
		fault   jobFault
		phase   string
		message string
	}{
		{
			name: "a pod that has not started",
			fault: jobFault{reason: reasonJobNotStarted, job: "movies-walk-2", pod: "movies-walk-2-pod",
				cause: "GitVolumeRefused: readOnly: a claim on this driver has to be mounted read-only"},
			phase: phaseBlocked,
			message: "the pod movies-walk-2-pod of the Job movies-walk-2 has not started: " +
				"GitVolumeRefused: readOnly: a claim on this driver has to be mounted read-only",
		},
		{
			name:    "a Job with no pod",
			fault:   jobFault{reason: reasonJobNotStarted, job: "movies-walk-2"},
			phase:   phaseBlocked,
			message: "the Job movies-walk-2 has no pod",
		},
		{
			name: "a Job that failed",
			fault: jobFault{reason: reasonJobFailed, job: "movies-walk-2",
				cause: "DeadlineExceeded: Job was active longer than specified deadline"},
			phase: phaseFailed,
			message: "the Job movies-walk-2 failed: " +
				"DeadlineExceeded: Job was active longer than specified deadline",
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			seen := scanning()
			seen.report = &libraryReport{Titles: 412}
			seen.fault = &one.fault

			status := deriveLibraryStatus(studioMovies(), seen, testNow)

			ready := conditionOf(t, status, conditionReady)
			if ready.Status != ConditionFalse || ready.Reason != one.fault.reason || ready.Message != one.message {
				t.Errorf("Ready = %+v, want False with %s: %q", ready, one.fault.reason, one.message)
			}
			if status.Phase != one.phase {
				t.Errorf("phase = %q, want %q", status.Phase, one.phase)
			}
		})
	}
}

// A placed pod that has not started carries no reason of the scheduler's, so
// the pass reads the newest Warning event of the pod and names it.
func TestReconcileNamesTheNewestWarningOfAPodThatHasNotStarted(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	late := testNow.Add(-jobStartGrace - time.Minute)
	jobs := []Job{moviesJob("movies-walk-2", late)}
	pods := []Pod{jobPod("movies-walk-2", podPending, placedAt(late))}
	cluster.events = []Event{
		warningAbout("movies-walk-2-pod", "FailedMount", "the older refusal", late),
		warningAbout("movies-walk-2-pod", "GitVolumeRefused", "readOnly: mount the claim read-only", testNow),
		warningAbout("series-walk-2-pod", "FailedMount", "another pod's refusal", testNow.Add(time.Minute)),
	}

	if err := testOperator(t, cluster).reconcile(t.Context(), library, standingCatalog(),
		jobs, pods, nil, testNow); err != nil {
		t.Fatal(err)
	}

	ready := conditionOf(t, cluster.heldLibrary("movies").Status, conditionReady)
	want := "the pod movies-walk-2-pod of the Job movies-walk-2 has not started: " +
		"GitVolumeRefused: readOnly: mount the claim read-only"
	if ready.Reason != reasonJobNotStarted || ready.Message != want {
		t.Errorf("Ready = %+v, want JobNotStarted: %q", ready, want)
	}
}

// a Warning event about one pod in the house namespace, last seen at the time
// given.
func warningAbout(pod, reason, message string, at time.Time) Event {
	return Event{
		Metadata:       ObjectMeta{Namespace: "house"},
		InvolvedObject: ObjectReference{Name: pod},
		Type:           eventWarning,
		Reason:         reason,
		Message:        message,
		LastTimestamp:  at,
	}
}

// An event written through the events.k8s.io API carries its time in
// eventTime or in its series, and the newest of the three times orders it.
func TestNewestEventReadsEveryTimeAnEventCarries(t *testing.T) {
	cases := []struct {
		name  string
		event Event
	}{
		{name: "lastTimestamp", event: Event{Reason: "newest", LastTimestamp: testNow}},
		{name: "eventTime", event: Event{Reason: "newest", EventTime: testNow}},
		{name: "series", event: Event{Reason: "newest",
			Series: &EventSeries{LastObservedTime: testNow}}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			older := Event{Reason: "older", LastTimestamp: testNow.Add(-time.Minute)}

			newest := newestEvent([]Event{older, one.event, older})

			if newest == nil || newest.Reason != "newest" {
				t.Errorf("newest = %+v, want the event with the reason newest", newest)
			}
		})
	}
}

// Kubernetes gives a reason, a sentence, or both, and the status prints what
// it gave.
func TestKubernetesWordsLeaveOutWhatIsEmpty(t *testing.T) {
	cases := []struct {
		reason, message, words string
	}{
		{reason: "DeadlineExceeded", message: "", words: "DeadlineExceeded"},
		{reason: "", message: "the pod has not started", words: "the pod has not started"},
		{reason: "FailedMount", message: "timed out", words: "FailedMount: timed out"},
	}
	for _, one := range cases {
		if got := kubernetesWords(one.reason, one.message); got != one.words {
			t.Errorf("kubernetesWords(%q, %q) = %q, want %q", one.reason, one.message, got, one.words)
		}
	}
}

// An events read that fails leaves the reason out, and the status still
// names the pod and the Job.
func TestReconcileNamesAStuckJobWhenTheEventsReadFails(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	cluster.broken[eventsPath("house")] = 500
	late := testNow.Add(-jobStartGrace - time.Minute)
	jobs := []Job{moviesJob("movies-walk-2", late)}
	pods := []Pod{jobPod("movies-walk-2", podPending, placedAt(late))}

	if err := testOperator(t, cluster).reconcile(t.Context(), library, standingCatalog(),
		jobs, pods, nil, testNow); err != nil {
		t.Fatal(err)
	}

	ready := conditionOf(t, cluster.heldLibrary("movies").Status, conditionReady)
	want := "the pod movies-walk-2-pod of the Job movies-walk-2 has not started"
	if ready.Reason != reasonJobNotStarted || ready.Message != want {
		t.Errorf("Ready = %+v, want JobNotStarted: %q", ready, want)
	}
}
