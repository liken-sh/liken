package main

// what these tests read: the derivation on its own, one set of facts in
// and one status out. They cover the reasons the two conditions carry
// and the rule that keeps a settled Library quiet.

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

// scanning is the whole path working: the storage bound, the
// namespace's catalog pod up, the schedule standing, and the reporter
// on the bus. Every case below changes one thing about it.
func scanning() libraryObservation {
	return libraryObservation{
		bound:             boundVolume(),
		choice:            standingCatalog(),
		cronJob:           testScanCronJob(studioMovies()),
		online:            true,
		operatorNamespace: testOperatorNamespace,
	}
}

// a running catalog pod, a standing schedule, and a report on the desk
// is the whole path working, and the status carries the reporter's
// counts, times, and runs as it published them.
func TestDeriveStatusCarriesTheReport(t *testing.T) {
	walked := time.Date(2026, 8, 29, 11, 30, 0, 0, time.UTC)
	changed := time.Date(2026, 8, 29, 11, 45, 0, 0, time.UTC)
	seen := scanning()
	seen.report = &libraryReport{Titles: 412, Unidentified: 3, RemovedLastSweep: 5,
		Items: 1204, Files: 6621, LastWalk: walked, LastChange: changed,
		Runs: []libraryRun{{Worker: workerScan, Job: "movies-scan-29380000", Finished: walked}}}

	status := deriveLibraryStatus(studioMovies(), seen, testNow)

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
	if len(status.Runs) != 1 || status.Runs[0].Job != "movies-scan-29380000" {
		t.Errorf("runs = %+v, want the run the reporter published", status.Runs)
	}
	ready := conditionOf(t, status, conditionReady)
	if ready.Status != ConditionTrue || ready.Reason != reasonReady {
		t.Errorf("Ready = %+v, want True", ready)
	}
	if ready.ObservedGeneration != 3 {
		t.Errorf("observedGeneration = %d, want the Library's generation", ready.ObservedGeneration)
	}
}

// the address is reported on the condition the schedule is written on:
// the storage is bound, and the namespace holds one Catalog. It does
// not wait on the catalog pod, so a roll of that pod leaves it in
// place.
func TestDeriveStatusReportsTheWebhookAddress(t *testing.T) {
	unbound := scanning()
	unbound.bound = binding{reason: reasonClaimUnbound}
	noCatalog := scanning()
	noCatalog.choice = singleCatalog(nil)
	rolling := scanning()
	rolling.choice = catalogChoice{catalog: testNamespaceCatalog()}

	cases := []struct {
		name    string
		seen    libraryObservation
		webhook string
	}{
		{name: "a library that is scanned", seen: scanning(),
			webhook: "http://library-operator.liken-system.svc/webhook/house/movies"},
		{name: "a catalog pod this pass replaced", seen: rolling,
			webhook: "http://library-operator.liken-system.svc/webhook/house/movies"},
		{name: "storage that is not bound", seen: unbound},
		{name: "a namespace with no Catalog", seen: noCatalog},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			status := deriveLibraryStatus(studioMovies(), one.seen, testNow)

			if status.Webhook != one.webhook {
				t.Errorf("webhook = %q, want %q", status.Webhook, one.webhook)
			}
		})
	}
}

// each reason names the step that has not happened, so a person reads
// the condition and knows where to look.
func TestReadyConditionNamesTheStepThatIsMissing(t *testing.T) {
	unbound := scanning()
	unbound.bound = binding{reason: reasonClaimUnbound}

	noCatalog := scanning()
	noCatalog.choice = singleCatalog(nil)

	twoCatalogs := scanning()
	twoCatalogs.choice = singleCatalog([]*NamespaceCatalog{
		{Metadata: ObjectMeta{Name: "one"}}, {Metadata: ObjectMeta{Name: "two"}},
	})

	noPod := scanning()
	noPod.choice = catalogChoice{catalog: testNamespaceCatalog()}

	failedPod := scanning()
	failed := readyCatalogPod("house-catalog", "house")
	failed.Status.Phase = podFailed
	failed.Status.Reason = "Evicted"
	failedPod.choice = catalogChoice{catalog: testNamespaceCatalog(), pod: failed}

	startingPod := scanning()
	starting := readyCatalogPod("house-catalog", "house")
	starting.Status.ContainerStatuses[0].Ready = false
	startingPod.choice = catalogChoice{catalog: testNamespaceCatalog(), pod: starting}

	noSchedule := scanning()
	noSchedule.cronJob = nil

	offline := scanning()
	offline.online = false

	cases := []struct {
		name    string
		seen    libraryObservation
		reason  string
		message string
	}{
		{
			name: "no volume", seen: unbound,
			reason: reasonNotBound, message: "the library's storage is not bound",
		},
		{
			name: "no Catalog in the namespace", seen: noCatalog,
			reason: reasonNoCatalog, message: "the namespace has no Catalog",
		},
		{
			name: "more than one Catalog in the namespace", seen: twoCatalogs,
			reason: reasonManyCatalogs,
			message: "the namespace has 2 Catalogs (one, two); " +
				"the operator stands none until one remains",
		},
		{
			name: "no catalog pod", seen: noPod,
			reason: reasonCatalogPending, message: "there is no catalog pod yet",
		},
		{
			name: "a catalog pod the kubelet gave up on", seen: failedPod,
			reason: reasonCatalogPending, message: "the catalog pod failed: Evicted",
		},
		{
			name: "a catalog pod whose containers are not ready", seen: startingPod,
			reason:  reasonCatalogPending,
			message: "the catalog pod runs and not every container is ready",
		},
		{
			name: "no schedule yet", seen: noSchedule,
			reason: reasonScanPending, message: "the scan schedule does not stand yet",
		},
		{
			name: "a reporter that left the bus", seen: offline,
			reason: reasonOffline, message: "the namespace's reporter is not on the bus",
		},
		{
			name: "a library the reporter has not reported", seen: scanning(),
			reason: reasonNoReport, message: "the reporter has not reported this library yet",
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			status := deriveLibraryStatus(studioMovies(), one.seen, testNow)

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

// the phase takes the first of the four values that holds, read from
// the Ready condition the same derivation built.
func TestThePhaseSaysWhatTheLibraryIsDoing(t *testing.T) {
	walking := &libraryReport{Titles: 412, Walking: true}
	between := &libraryReport{Titles: 412}

	cases := []struct {
		name   string
		change func(seen *libraryObservation)
		phase  string
	}{
		{
			name: "a catalog pod that has not started",
			change: func(seen *libraryObservation) {
				seen.choice = catalogChoice{catalog: testNamespaceCatalog()}
				seen.report = between
			},
			phase: phasePending,
		},
		{
			name:   "a library the reporter has not reported",
			change: func(seen *libraryObservation) {},
			phase:  phasePending,
		},
		{
			name: "a reporter that left the bus",
			change: func(seen *libraryObservation) {
				seen.report = between
				seen.online = false
			},
			phase: phaseOffline,
		},
		{
			name:   "a walk in flight",
			change: func(seen *libraryObservation) { seen.report = walking },
			phase:  phaseScanning,
		},
		{
			name:   "between walks",
			change: func(seen *libraryObservation) { seen.report = between },
			phase:  phaseIdle,
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			seen := scanning()
			one.change(&seen)

			status := deriveLibraryStatus(studioMovies(), seen, testNow)

			if status.Phase != one.phase {
				t.Errorf("phase = %q, want %q", status.Phase, one.phase)
			}
		})
	}
}

// a catalog pod that reports no container at all is still starting, so
// it is not ready.
func TestReadyConditionRefusesAPodTheKubeletHasNotSpokenFor(t *testing.T) {
	seen := scanning()
	seen.choice = catalogChoice{
		catalog: testNamespaceCatalog(),
		pod: &Pod{
			Metadata: ObjectMeta{Name: "house-catalog-catalog"},
			Status:   PodStatus{Phase: podRunning},
		},
	}

	condition := readyCondition(seen, 1)

	if condition.Status != ConditionFalse || condition.Reason != reasonCatalogPending {
		t.Errorf("Ready = %+v, want False with CatalogPending", condition)
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
			want:   "the catalog pod failed: the node is out of memory",
		},
		{
			name:   "a reason alone",
			status: PodStatus{Reason: "Evicted"},
			want:   "the catalog pod failed: Evicted",
		},
		{name: "neither", status: PodStatus{}, want: "the catalog pod failed"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := podFailureMessage(&Pod{Status: one.status}); got != one.want {
				t.Errorf("message = %q, want %q", got, one.want)
			}
		})
	}
}

// the ordinary hold on a pending pod is the volume, so the kubelet's
// own sentence is preferred over this operator's.
func TestPodPendingMessagePrefersTheKubeletsWords(t *testing.T) {
	pending := &Pod{Status: PodStatus{Phase: podPending, Message: "no node can mount the volume"}}

	if got := podPendingMessage(pending); got != "no node can mount the volume" {
		t.Errorf("message = %q, want the kubelet's own words", got)
	}
	if got := podPendingMessage(&Pod{Status: PodStatus{Phase: podPending}}); got != "the catalog pod has not started" {
		t.Errorf("message = %q, want the bare fact", got)
	}
}

// A condition's lastTransitionTime moves only when the verdict flips,
// which is what lets a reader ask how long a library has been Ready.
func TestDeriveStatusKeepsTheTimeOfTheLastFlip(t *testing.T) {
	library := studioMovies()
	seen := scanning()
	seen.report = &libraryReport{Titles: 12}
	library.Status = deriveLibraryStatus(library, seen, testNow)

	later := deriveLibraryStatus(library, seen, testNow.Add(time.Hour))

	if got := conditionOf(t, later, conditionReady).LastTransitionTime; !got.Equal(testNow) {
		t.Errorf("lastTransitionTime = %v, want the time of the flip %v", got, testNow)
	}
}

// The derivation reads the Library and writes nothing through it, so
// the comparison that decides whether to write reads the status the
// Library still carries.
func TestDeriveStatusLeavesTheLibraryAlone(t *testing.T) {
	library := studioMovies()
	seen := scanning()
	seen.report = &libraryReport{Titles: 12}
	library.Status = deriveLibraryStatus(library, seen, testNow)
	before := conditionOf(t, library.Status, conditionReady)

	unbound := scanning()
	unbound.bound = binding{reason: reasonClaimNotFound}
	deriveLibraryStatus(library, unbound, testNow)

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
	seen := scanning()
	seen.report = &libraryReport{Titles: 12}
	settled := deriveLibraryStatus(library, seen, testNow)

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
// the status again from the fresh copy. A write the API server refuses
// for any other reason is a failure the pass reports.
func TestWriteLibraryStatusAnswersTheServersRefusal(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "a conflict is success", status: http.StatusConflict},
		{name: "any other refusal is a failure", status: http.StatusInternalServerError, wantErr: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := boundHouse(cluster)
			cluster.broken["/apis/"+libraryAPIVersion+"/namespaces/house/libraries/movies/status"] = one.status
			client := testOperator(t, cluster).client

			err := writeLibraryStatus(t.Context(), client, library,
				deriveLibraryStatus(library, scanning(), testNow))

			if one.wantErr && err == nil {
				t.Fatal("err = nil, want the server's refusal")
			}
			if !one.wantErr && err != nil {
				t.Fatalf("err = %v, want a conflict to read as success", err)
			}
		})
	}
}

// A git library reports the commit its last successful scan read.
// status.commit is the scan run's own commit, so the operator reads it from
// the catalog and never from a checkout.
func TestDeriveStatusCarriesTheCommitTheScanRead(t *testing.T) {
	seen := scanning()
	seen.report = &libraryReport{Runs: []libraryRun{{
		Worker: workerScan, Job: "franchises-scan-1", Finished: testNow,
		Commit: "9b1c0a7f2d3e4b5a6c7d8e9f0a1b2c3d4e5f6a7b",
	}}}

	status := deriveLibraryStatus(studioMovies(), seen, testNow)

	if status.Commit != "9b1c0a7f2d3e4b5a6c7d8e9f0a1b2c3d4e5f6a7b" {
		t.Errorf("commit = %q, want the one the scan run read", status.Commit)
	}
	if status.Phase != phaseIdle {
		t.Errorf("phase = %q, want %q for a run that carries no failure", status.Phase, phaseIdle)
	}
}

// A scan that failed leaves the tables as they were, and the phase says so
// until the next scan succeeds. A franchises library reads Failed when the
// clone could not reach the forge.
func TestDeriveStatusReadsFailedWhileTheScanRunCarriesAFailure(t *testing.T) {
	seen := scanning()
	seen.report = &libraryReport{Runs: []libraryRun{{
		Worker: workerScan, Job: "franchises-scan-2", Finished: testNow,
		Commit: "9b1c0a7f", Failure: "could not clone the repository",
	}}}

	status := deriveLibraryStatus(studioMovies(), seen, testNow)

	if status.Phase != phaseFailed {
		t.Errorf("phase = %q, want %q", status.Phase, phaseFailed)
	}
	if status.Commit != "9b1c0a7f" {
		t.Errorf("commit = %q, want the commit the last good scan read", status.Commit)
	}
}
