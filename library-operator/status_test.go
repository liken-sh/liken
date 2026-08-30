package main

// These tests read the derivation on its own: one set of facts in, one
// status out. They cover the reasons the two conditions carry and the
// rule that keeps a settled Library quiet.

import (
	"net/http"
	"testing"
	"time"
)

// The time every derived status is stamped with, so a test compares
// two derivations without the clock between them.
var testNow = time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)

// boundVolume is the binding a bound claim produces, the state the
// Ready condition is read against.
func boundVolume() binding {
	return binding{
		volume:  &LibraryVolume{Name: "pv-movies", Type: "nfs"},
		reason:  reasonBound,
		message: "the claim movies is bound to the PersistentVolume pv-movies",
	}
}

// readyPod is a scanner pod as the kubelet reports one that works.
func readyPod(phase string, ready bool) *Pod {
	return &Pod{
		Metadata: ObjectMeta{Name: "movies-scanner", Namespace: "house"},
		Status: PodStatus{Phase: phase, ContainerStatuses: []ContainerStatus{
			{Name: scannerContainer, Ready: ready},
			{Name: catalogContainer, Ready: ready},
		}},
	}
}

// A running pod with every container ready and a report on the desk is
// the whole path working, and the status carries the scanner's counts
// and times as it reported them.
func TestDeriveStatusCarriesTheScannersReport(t *testing.T) {
	walked := time.Date(2026, 8, 29, 11, 30, 0, 0, time.UTC)
	changed := time.Date(2026, 8, 29, 11, 45, 0, 0, time.UTC)
	report := &libraryReport{Titles: 412, Unidentified: 3, LastWalk: walked, LastChange: changed}

	status := deriveLibraryStatus(studioMovies(), boundVolume(), readyPod(podRunning, true), report, testNow)

	if status.Titles != 412 || status.Unidentified != 3 {
		t.Errorf("counts = %d and %d, want 412 and 3", status.Titles, status.Unidentified)
	}
	if !status.LastWalk.Equal(walked) || !status.LastChange.Equal(changed) {
		t.Errorf("times = %v and %v, want %v and %v",
			status.LastWalk, status.LastChange, walked, changed)
	}
	if status.Pod != "movies-scanner" {
		t.Errorf("pod = %q, want movies-scanner", status.Pod)
	}
	ready := conditionOf(t, status, conditionReady)
	if ready.Status != ConditionTrue || ready.Reason != reasonReady {
		t.Errorf("Ready = %+v, want True", ready)
	}
	if ready.ObservedGeneration != 3 {
		t.Errorf("observedGeneration = %d, want the Library's generation", ready.ObservedGeneration)
	}
}

// Each reason names the step that has not happened, so a person reads
// the condition and knows where to look.
func TestReadyConditionNamesTheStepThatIsMissing(t *testing.T) {
	failed := readyPod(podFailed, false)
	failed.Status.Reason = "Evicted"
	unschedulable := readyPod(podPending, false)
	unschedulable.Status.Message = "no node can mount the volume"

	cases := []struct {
		name    string
		bound   binding
		pod     *Pod
		report  *libraryReport
		reason  string
		message string
	}{
		{
			name:    "no volume",
			bound:   binding{reason: reasonClaimUnbound},
			reason:  reasonNotBound,
			message: "the library's storage is not bound",
		},
		{
			name:    "no pod",
			bound:   boundVolume(),
			reason:  reasonPodPending,
			message: "there is no scanner pod yet",
		},
		{
			name:    "a pod that has not started",
			bound:   boundVolume(),
			pod:     readyPod(podPending, false),
			reason:  reasonPodPending,
			message: "the scanner pod has not started",
		},
		{
			name:    "a pod no node will take",
			bound:   boundVolume(),
			pod:     unschedulable,
			reason:  reasonPodPending,
			message: "no node can mount the volume",
		},
		{
			name:    "a pod whose containers are not ready",
			bound:   boundVolume(),
			pod:     readyPod(podRunning, false),
			reason:  reasonPodPending,
			message: "the scanner pod runs and not every container is ready",
		},
		{
			name:    "a pod the kubelet gave up on",
			bound:   boundVolume(),
			pod:     failed,
			reason:  reasonPodFailed,
			message: "the scanner pod failed: Evicted",
		},
		{
			name:    "a scanner that has not reported",
			bound:   boundVolume(),
			pod:     readyPod(podRunning, true),
			reason:  reasonNoReport,
			message: "the scanner has not reported yet",
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			status := deriveLibraryStatus(studioMovies(), one.bound, one.pod, one.report, testNow)

			ready := conditionOf(t, status, conditionReady)
			if ready.Status != ConditionFalse {
				t.Errorf("Ready = %+v, want False", ready)
			}
			if ready.Reason != one.reason {
				t.Errorf("reason = %q, want %q", ready.Reason, one.reason)
			}
			if ready.Message != one.message {
				t.Errorf("message = %q, want %q", ready.Message, one.message)
			}
		})
	}
}

// A pod that reports no container at all is still starting, so it is
// not ready.
func TestReadyConditionRefusesAPodTheKubeletHasNotSpokenFor(t *testing.T) {
	silent := &Pod{
		Metadata: ObjectMeta{Name: "movies-scanner"},
		Status:   PodStatus{Phase: podRunning},
	}

	condition := readyCondition(boundVolume(), silent, &libraryReport{}, 1)

	if condition.Status != ConditionFalse || condition.Reason != reasonPodPending {
		t.Errorf("Ready = %+v, want False with PodPending", condition)
	}
}

// A failure the kubelet describes in its own words is reported in
// those words, because they are the part a person acts on.
func TestPodFailureMessagePrefersTheKubeletsWords(t *testing.T) {
	cases := []struct {
		name   string
		status PodStatus
		want   string
	}{
		{
			name:   "a message",
			status: PodStatus{Message: "the node is out of memory"},
			want:   "the scanner pod failed: the node is out of memory",
		},
		{
			name:   "a reason alone",
			status: PodStatus{Reason: "Evicted"},
			want:   "the scanner pod failed: Evicted",
		},
		{name: "neither", status: PodStatus{}, want: "the scanner pod failed"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := podFailureMessage(&Pod{Status: one.status}); got != one.want {
				t.Errorf("message = %q, want %q", got, one.want)
			}
		})
	}
}

// A condition's lastTransitionTime moves only when the verdict flips,
// which is what lets a reader ask how long a library has been Ready.
func TestDeriveStatusKeepsTheTimeOfTheLastFlip(t *testing.T) {
	library := studioMovies()
	report := &libraryReport{Titles: 12}
	library.Status = deriveLibraryStatus(library, boundVolume(), readyPod(podRunning, true), report, testNow)

	later := deriveLibraryStatus(library, boundVolume(), readyPod(podRunning, true), report,
		testNow.Add(time.Hour))

	if got := conditionOf(t, later, conditionReady).LastTransitionTime; !got.Equal(testNow) {
		t.Errorf("lastTransitionTime = %v, want the time of the flip %v", got, testNow)
	}
}

// The derivation reads the Library and writes nothing through it, so
// the comparison that decides whether to write reads the status the
// Library still carries.
func TestDeriveStatusLeavesTheLibraryAlone(t *testing.T) {
	library := studioMovies()
	library.Status = deriveLibraryStatus(library, boundVolume(), readyPod(podRunning, true),
		&libraryReport{Titles: 12}, testNow)
	before := conditionOf(t, library.Status, conditionReady)

	deriveLibraryStatus(library, binding{reason: reasonClaimNotFound}, nil, nil, testNow)

	if got := conditionOf(t, library.Status, conditionReady); got != before {
		t.Errorf("the Library's own condition changed to %+v", got)
	}
}

// A status that says what the Library already says is not written at
// all, and a status that differs is.
func TestWriteLibraryStatusWritesOnlyAChange(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	client := testOperator(t, cluster).client
	settled := deriveLibraryStatus(library, boundVolume(), readyPod(podRunning, true),
		&libraryReport{Titles: 12}, testNow)

	if err := writeLibraryStatus(client, library, settled); err != nil {
		t.Fatal(err)
	}
	if err := writeLibraryStatus(client, library, settled); err != nil {
		t.Fatal(err)
	}
	if got := cluster.countRequests(http.MethodPut, "libraries"); got != 1 {
		t.Fatalf("status writes = %d, want one", got)
	}

	settled.Titles = 13
	if err := writeLibraryStatus(client, library, settled); err != nil {
		t.Fatal(err)
	}
	if got := cluster.countRequests(http.MethodPut, "libraries"); got != 2 {
		t.Errorf("status writes = %d, want two", got)
	}
}

// A write that another writer got to first is not a failure. That
// write wakes this operator's own watch, and the pass it wakes derives
// the status again from the fresh copy.
func TestWriteLibraryStatusAcceptsAConflict(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	cluster.broken["/apis/"+libraryAPIVersion+"/namespaces/house/libraries/movies/status"] = http.StatusConflict
	client := testOperator(t, cluster).client

	err := writeLibraryStatus(client, library,
		deriveLibraryStatus(library, boundVolume(), nil, nil, testNow))

	if err != nil {
		t.Fatalf("err = %v, want a conflict to read as success", err)
	}
}

// A write the API server refuses for any other reason is a failure the
// pass reports.
func TestWriteLibraryStatusReportsAFailedWrite(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	cluster.broken["/apis/"+libraryAPIVersion+"/namespaces/house/libraries/movies/status"] = http.StatusInternalServerError
	client := testOperator(t, cluster).client

	err := writeLibraryStatus(client, library,
		deriveLibraryStatus(library, boundVolume(), nil, nil, testNow))

	if err == nil {
		t.Fatal("err = nil, want the server's refusal")
	}
}
