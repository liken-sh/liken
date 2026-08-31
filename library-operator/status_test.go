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
		Status: PodStatus{
			Phase:                 phase,
			InitContainerStatuses: []ContainerStatus{{Name: catalogContainer, Ready: ready}},
			ContainerStatuses:     []ContainerStatus{{Name: scannerContainer, Ready: ready}},
		},
	}
}

// A running pod with every container ready and a report on the desk is
// the whole path working, and the status carries the scanner's counts
// and times as it reported them.
func TestDeriveStatusCarriesTheScannersReport(t *testing.T) {
	walked := time.Date(2026, 8, 29, 11, 30, 0, 0, time.UTC)
	changed := time.Date(2026, 8, 29, 11, 45, 0, 0, time.UTC)
	report := &libraryReport{Titles: 412, Unidentified: 3, RemovedLastSweep: 5,
		Items: 1204, Files: 6621, LastWalk: walked, LastChange: changed}

	status := deriveLibraryStatus(studioMovies(), boundVolume(), withCatalog(), readyPod(podRunning, true), report, true, testNow)

	if status.Titles != 412 || status.Unidentified != 3 {
		t.Errorf("counts = %d and %d, want 412 and 3", status.Titles, status.Unidentified)
	}
	if status.Items != 1204 || status.Files != 6621 {
		t.Errorf("catalog counts = %d items and %d files, want 1204 and 6621", status.Items, status.Files)
	}
	if status.RemovedLastSweep != 5 {
		t.Errorf("removedLastSweep = %d, want the count the sweep removed", status.RemovedLastSweep)
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
// The address is reported on the same condition the operator writes the
// Service on: the storage is bound, and the namespace holds one Catalog. It
// does not wait on the pod, so a replacement of the pod leaves it in place.
func TestDeriveStatusReportsTheWebhookAddress(t *testing.T) {
	cases := []struct {
		name    string
		bound   binding
		choice  catalogChoice
		pod     *Pod
		webhook string
	}{
		{name: "a scanner that stands", bound: boundVolume(), choice: withCatalog(),
			pod: readyPod(podRunning, true), webhook: "http://movies-scanner.house.svc:8090/"},
		{name: "a pod this pass replaced", bound: boundVolume(), choice: withCatalog(),
			webhook: "http://movies-scanner.house.svc:8090/"},
		{name: "storage that is not bound", choice: withCatalog()},
		{name: "a namespace with no Catalog", bound: boundVolume(), choice: singleCatalog(nil)},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			status := deriveLibraryStatus(studioMovies(), one.bound, one.choice, one.pod, nil, true, testNow)

			if status.Webhook != one.webhook {
				t.Errorf("webhook = %q, want %q", status.Webhook, one.webhook)
			}
		})
	}
}

func TestReadyConditionNamesTheStepThatIsMissing(t *testing.T) {
	failed := readyPod(podFailed, false)
	failed.Status.Reason = "Evicted"
	unschedulable := readyPod(podPending, false)
	unschedulable.Status.Message = "no node can mount the volume"

	cases := []struct {
		name    string
		bound   binding
		choice  catalogChoice
		pod     *Pod
		report  *libraryReport
		reason  string
		message string
	}{
		{
			name:    "no volume",
			bound:   binding{reason: reasonClaimUnbound},
			choice:  withCatalog(),
			reason:  reasonNotBound,
			message: "the library's storage is not bound",
		},
		{
			name:    "no Catalog in the namespace",
			bound:   boundVolume(),
			choice:  singleCatalog(nil),
			reason:  reasonNoCatalog,
			message: "the namespace has no Catalog",
		},
		{
			name:   "more than one Catalog in the namespace",
			bound:  boundVolume(),
			choice: singleCatalog([]*NamespaceCatalog{{Metadata: ObjectMeta{Name: "one"}}, {Metadata: ObjectMeta{Name: "two"}}}),
			reason: reasonManyCatalogs,
			message: "the namespace has 2 Catalogs (one, two); " +
				"the operator stands none until one remains",
		},
		{
			name:    "no pod",
			bound:   boundVolume(),
			choice:  withCatalog(),
			reason:  reasonPodPending,
			message: "there is no scanner pod yet",
		},
		{
			name:    "a pod that has not started",
			bound:   boundVolume(),
			choice:  withCatalog(),
			pod:     readyPod(podPending, false),
			reason:  reasonPodPending,
			message: "the scanner pod has not started",
		},
		{
			name:    "a pod no node will take",
			bound:   boundVolume(),
			choice:  withCatalog(),
			pod:     unschedulable,
			reason:  reasonPodPending,
			message: "no node can mount the volume",
		},
		{
			name:    "a pod whose containers are not ready",
			bound:   boundVolume(),
			choice:  withCatalog(),
			pod:     readyPod(podRunning, false),
			reason:  reasonPodPending,
			message: "the scanner pod runs and not every container is ready",
		},
		{
			name:    "a pod the kubelet gave up on",
			bound:   boundVolume(),
			choice:  withCatalog(),
			pod:     failed,
			reason:  reasonPodFailed,
			message: "the scanner pod failed: Evicted",
		},
		{
			name:    "a scanner that has not reported",
			bound:   boundVolume(),
			choice:  withCatalog(),
			pod:     readyPod(podRunning, true),
			reason:  reasonNoReport,
			message: "the scanner has not reported yet",
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			status := deriveLibraryStatus(studioMovies(), one.bound, one.choice, one.pod, one.report, true, testNow)

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

// The phase takes the first of the four values that holds, read from the
// Ready condition the same derivation built.
func TestThePhaseSaysWhatTheScannerIsDoing(t *testing.T) {
	walking := &libraryReport{Titles: 412, Walking: true}
	between := &libraryReport{Titles: 412}

	cases := []struct {
		name   string
		pod    *Pod
		report *libraryReport
		online bool
		phase  string
	}{
		{name: "a pod that has not started", pod: readyPod(podPending, false), report: between, online: true, phase: phasePending},
		{name: "a scanner that has not reported", pod: readyPod(podRunning, true), online: true, phase: phasePending},
		{name: "a scanner that left the bus", pod: readyPod(podRunning, true), report: between, phase: phaseOffline},
		{name: "a walk in flight", pod: readyPod(podRunning, true), report: walking, online: true, phase: phaseScanning},
		{name: "between walks", pod: readyPod(podRunning, true), report: between, online: true, phase: phaseIdle},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			status := deriveLibraryStatus(studioMovies(), boundVolume(), withCatalog(), one.pod, one.report, one.online, testNow)

			if status.Phase != one.phase {
				t.Errorf("phase = %q, want %q", status.Phase, one.phase)
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

	condition := readyCondition(boundVolume(), withCatalog(), silent, &libraryReport{}, 1)

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
	library.Status = deriveLibraryStatus(library, boundVolume(), withCatalog(), readyPod(podRunning, true), report, true, testNow)

	later := deriveLibraryStatus(library, boundVolume(), withCatalog(), readyPod(podRunning, true), report, true,
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
	library.Status = deriveLibraryStatus(library, boundVolume(), withCatalog(), readyPod(podRunning, true),
		&libraryReport{Titles: 12}, true, testNow)
	before := conditionOf(t, library.Status, conditionReady)

	deriveLibraryStatus(library, binding{reason: reasonClaimNotFound}, withCatalog(), nil, nil, true, testNow)

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
	settled := deriveLibraryStatus(library, boundVolume(), withCatalog(), readyPod(podRunning, true),
		&libraryReport{Titles: 12}, true, testNow)

	if err := writeLibraryStatus(t.Context(), client, library, settled); err != nil {
		t.Fatal(err)
	}
	if err := writeLibraryStatus(t.Context(), client, library, settled); err != nil {
		t.Fatal(err)
	}
	if got := cluster.countRequests(http.MethodPut, "libraries"); got != 1 {
		t.Fatalf("status writes = %d, want one", got)
	}

	settled.Titles = 13
	if err := writeLibraryStatus(t.Context(), client, library, settled); err != nil {
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

	err := writeLibraryStatus(t.Context(), client, library,
		deriveLibraryStatus(library, boundVolume(), withCatalog(), nil, nil, true, testNow))

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

	err := writeLibraryStatus(t.Context(), client, library,
		deriveLibraryStatus(library, boundVolume(), withCatalog(), nil, nil, true, testNow))

	if err == nil {
		t.Fatal("err = nil, want the server's refusal")
	}
}
