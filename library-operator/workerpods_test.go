package main

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
)

// workerPod builds one pod of the titles worker of the movies Library, as
// the Job controller leaves it: with the worker's labels, the Job's name,
// and the completion index of an Indexed Job. The pod is Succeeded and its
// Job has counted it, so it holds no job-tracking finalizer. Each option
// changes one of those facts.
func workerPod(name string, options ...func(pod *Pod)) *Pod {
	labels := workerLabels("movies", factTitles)
	labels[jobNameLabel] = "movies-titles-29380000"
	labels[completionIndexLabel] = "0"
	pod := &Pod{
		Metadata: ObjectMeta{Name: name, Namespace: "house", Labels: labels},
		Status:   PodStatus{Phase: "Succeeded"},
	}
	for _, option := range options {
		option(pod)
	}
	return pod
}

// notCounted is a pod whose Job has not recorded it yet.
func notCounted(pod *Pod) { pod.Metadata.Finalizers = []string{jobTrackingFinalizer} }

// failedPod is a pod whose container exited nonzero.
func failedPod(pod *Pod) { pod.Status.Phase = "Failed" }

// notIndexed is the pod of a worker Job that is not Indexed, such as the fit
// of the titles model, which carries no completion index.
func notIndexed(pod *Pod) {
	delete(pod.Metadata.Labels, completionIndexLabel)
	pod.Metadata.Labels[workerLabelKey] = titlesFitMode
}

// A pass deletes a finished worker pod once its Job has counted it, and
// keeps every other worker pod: one the Job has not counted yet, one that
// failed, and the pod of a Job that is not Indexed.
func TestPassDeletesTheCountedPodsOfTheWorkers(t *testing.T) {
	cases := []struct {
		name  string
		pod   *Pod
		stays bool
	}{
		{name: "a counted pod that succeeded", pod: workerPod("movies-titles-0")},
		{name: "a pod the Job has not counted", pod: workerPod("movies-titles-0", notCounted), stays: true},
		{name: "a pod that failed", pod: workerPod("movies-titles-0", failedPod), stays: true},
		{name: "a pod of a Job that is not Indexed", pod: workerPod("movies-titles-0", notIndexed), stays: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := newFakeCluster()
				boundHouse(cluster)
				cluster.pods[one.pod.Metadata.Name] = one.pod
				operator := testOperator(t, cluster)
				operator.reports.fold("house", "movies", *scanRunReport())

				operator.pass()

				stays := cluster.heldPod("movies-titles-0") != nil
				if stays != one.stays {
					t.Errorf("the pod stays: %v, want %v", stays, one.stays)
				}
			})
		})
	}
}

// A pass writes one line for each Job whose pods it deleted, with the
// count, and no line for each pod.
func TestPassLogsOneLineForTheCountedPodsOfAJob(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		boundHouse(cluster)
		for _, name := range []string{"movies-titles-0", "movies-titles-1", "movies-titles-2"} {
			cluster.pods[name] = workerPod(name)
		}
		operator := testOperator(t, cluster)
		operator.reports.fold("house", "movies", *scanRunReport())
		logged := &bytes.Buffer{}
		operator.log = logged

		operator.pass()

		want := "library house/movies: deleted 3 finished pods of the titles worker movies-titles-29380000, " +
			"because the Job has counted them"
		if strings.Count(logged.String(), "finished pod") != 1 || !strings.Contains(logged.String(), want) {
			t.Errorf("log = %q, want one line %q", logged.String(), want)
		}
	})
}

// A delete the API server refuses is reported, and the pass goes on to the
// next pod.
func TestPassCarriesOnPastAWorkerPodItCannotDelete(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		boundHouse(cluster)
		cluster.pods["movies-titles-0"] = workerPod("movies-titles-0")
		cluster.pods["movies-titles-1"] = workerPod("movies-titles-1")
		cluster.broken[http.MethodDelete+" "+podsPath("house")+"/movies-titles-0"] = http.StatusInternalServerError
		operator := testOperator(t, cluster)
		operator.reports.fold("house", "movies", *scanRunReport())

		operator.pass()

		if cluster.heldPod("movies-titles-0") == nil || cluster.heldPod("movies-titles-1") != nil {
			t.Errorf("pods held: refused %v, deletable %v; want only the refused pod",
				cluster.heldPod("movies-titles-0") != nil, cluster.heldPod("movies-titles-1") != nil)
		}
	})
}
