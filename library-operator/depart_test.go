package main

// what these tests read: one departure pass against a real-shaped API
// server, rung by rung.

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// the Library every test starts from: house/movies deleting, finalizer
// held, catalog claim bound.
func departingMovies(cluster *fakeCluster) *Library {
	library := boundHouse(cluster)
	library.Metadata.DeletionTimestamp = "2026-08-31T12:00:00Z"
	library.Metadata.Finalizers = []string{libraryFinalizer}
	cluster.claims["movies-catalog"] = &PersistentVolumeClaim{
		Metadata: ObjectMeta{Name: "movies-catalog", Namespace: "house"},
		Status:   PersistentVolumeClaimStatus{Phase: claimBound},
	}
	return library
}

// one Job of the house namespace, in whatever state the Job controller
// would have left it.
func houseJob(name, worker string, status JobStatus) Job {
	return Job{
		Metadata: ObjectMeta{
			Name:      name,
			Namespace: "house",
			Labels:    workerLabels("movies", worker),
		},
		Status: status,
	}
}

// the walk the CronJob created, which the CronJob owns.
func walkJob(status JobStatus) Job {
	job := houseJob("movies-scan-29380000", workerScan, status)
	job.Metadata.OwnerReferences = []OwnerReference{{Kind: "CronJob", Name: "movies-scan"}}
	return job
}

// the report in which the namespace's reporter echoes one cleanup Job
// back, which is the proof the deletes reached the standing catalog.
func echoing(job string) libraryReport {
	return libraryReport{Runs: []libraryRun{
		{Worker: workerCleanup, Job: job, Started: testNow, Finished: testNow},
	}}
}

// reads the Departing condition off the held Library, where every rung of
// the ladder reports.
func departingCondition(t *testing.T, cluster *fakeCluster) Condition {
	t.Helper()
	library := cluster.heldLibrary("movies")
	if library == nil {
		t.Fatal("the Library is gone, so it reported no stage")
	}
	return conditionOf(t, library.Status, conditionDeparting)
}

// a Library without the finalizer takes it on the next pass, so old and
// recreated ones are held too.
func TestReconcileHoldsALibraryAgainstDeletion(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)

	if err := testOperator(t, cluster).reconcile(t.Context(), library, standingCatalog(), nil, testNow); err != nil {
		t.Fatal(err)
	}

	held := cluster.heldLibrary("movies")
	if !held.Metadata.holds(libraryFinalizer) {
		t.Errorf("finalizers = %v, want %s", held.Metadata.Finalizers, libraryFinalizer)
	}
	if got := cluster.countRequests(http.MethodPatch, "libraries"); got != 1 {
		t.Errorf("patches = %d, want one", got)
	}
}

// a Library that adopted the finalizer under its former name swaps to
// the current one in a single patch, so a Library held by release
// 2026.08.31-004 still deletes under this one.
func TestReconcileSwapsTheFormerFinalizer(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	library.Metadata.Finalizers = []string{formerLibraryFinalizer}

	if err := testOperator(t, cluster).reconcile(t.Context(), library, standingCatalog(), nil, testNow); err != nil {
		t.Fatal(err)
	}

	held := cluster.heldLibrary("movies")
	if !slices.Equal(held.Metadata.Finalizers, []string{libraryFinalizer}) {
		t.Errorf("finalizers = %v, want only %s", held.Metadata.Finalizers, libraryFinalizer)
	}
	if got := cluster.countRequests(http.MethodPatch, "libraries"); got != 1 {
		t.Errorf("patches = %d, want one", got)
	}
}

// a finalizer already held is not patched again, so the pass writes
// nothing on a settled object.
func TestReconcileDoesNotPatchAFinalizerItAlreadyHolds(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	library.Metadata.Finalizers = []string{libraryFinalizer}

	if err := testOperator(t, cluster).reconcile(t.Context(), library, standingCatalog(), nil, testNow); err != nil {
		t.Fatal(err)
	}

	if got := cluster.countRequests(http.MethodPatch, "libraries"); got != 0 {
		t.Errorf("patches = %d, want none", got)
	}
}

// a namespace with no Catalog holds no rows to sweep, so the departure
// releases at once. A deleting namespace ends the same way, because its
// Catalog goes with everything else.
func TestDepartReleasesWhenTheNamespaceHasNoCatalog(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)

	if err := testOperator(t, cluster).depart(t.Context(), library, singleCatalog(nil), nil); err != nil {
		t.Fatal(err)
	}

	if cluster.heldLibrary("movies") != nil {
		t.Error("the Library still stands, so the finalizer was not released")
	}
	if cluster.heldJob("house", "movies-cleanup") != nil {
		t.Error("a cleanup job stands for a library with nothing to clean")
	}
}

// a deleting Library that still holds only the former name departs and
// releases the same way, so no Library sticks on the retired name.
func TestDepartReleasesTheFormerFinalizer(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	library.Metadata.Finalizers = []string{formerLibraryFinalizer}

	if err := testOperator(t, cluster).depart(t.Context(), library, singleCatalog(nil), nil); err != nil {
		t.Fatal(err)
	}

	if cluster.heldLibrary("movies") != nil {
		t.Error("the Library still stands, so the former finalizer was not released")
	}
}

// a namespace with more than one Catalog names no cluster to sweep, so
// the departure waits for a person and says so.
func TestDepartBlocksOnANamespaceWithTwoCatalogs(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	choice := singleCatalog([]*NamespaceCatalog{
		{Metadata: ObjectMeta{Name: "one", Namespace: "house"}},
		{Metadata: ObjectMeta{Name: "two", Namespace: "house"}},
	})

	if err := testOperator(t, cluster).depart(t.Context(), library, choice, nil); err != nil {
		t.Fatal(err)
	}

	if cluster.heldLibrary("movies") == nil {
		t.Fatal("the finalizer was released with no cluster to sweep from")
	}
	stage := departingCondition(t, cluster)
	if stage.Reason != reasonBlocked || !strings.Contains(stage.Message, "2 Catalogs") {
		t.Errorf("Departing = %+v, want %s naming the conflict", stage, reasonBlocked)
	}
}

// the schedule goes first, so no walk starts behind the sweep.
func TestDepartStopsTheScheduleFirst(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	cluster.cronJobs["house/movies-scan"] = &CronJob{
		Metadata: ObjectMeta{Name: "movies-scan", Namespace: "house"},
	}

	if err := testOperator(t, cluster).depart(t.Context(), library, standingCatalog(), nil); err != nil {
		t.Fatal(err)
	}

	if cluster.heldCronJob("house", "movies-scan") != nil {
		t.Error("the scan schedule stands during a departure")
	}
}

// a scan that is still running rewrites the rows the sweep deletes and
// holds the claim the cleanup job needs, so the departure waits it out.
func TestDepartWaitsForARunningScan(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	jobs := []Job{walkJob(JobStatus{Active: 1})}

	if err := testOperator(t, cluster).depart(t.Context(), library, standingCatalog(), jobs); err != nil {
		t.Fatal(err)
	}

	if cluster.heldJob("house", "movies-cleanup") != nil {
		t.Error("the cleanup job stood while a scan was still running")
	}
	if stage := departingCondition(t, cluster); stage.Reason != reasonScanRunning {
		t.Errorf("Departing = %+v, want %s", stage, reasonScanRunning)
	}
	if phase := cluster.heldLibrary("movies").Status.Phase; phase != phaseDeparting {
		t.Errorf("phase = %q, want %s", phase, phaseDeparting)
	}
}

// a claim deleted while the library lived leaves the rows in the
// standing catalog, so the departure stands a fresh claim and sweeps
// from it.
func TestDepartStandsACatalogClaimTheSweepNeeds(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	delete(cluster.claims, "movies-catalog")

	if err := testOperator(t, cluster).depart(t.Context(), library, standingCatalog(), nil); err != nil {
		t.Fatal(err)
	}

	if cluster.heldClaim("movies-catalog") == nil {
		t.Error("the pass stood no catalog claim for the cleanup job to mount")
	}
	if cluster.heldJob("house", "movies-cleanup") == nil {
		t.Error("the pass stood no cleanup job")
	}
}

// with the scans quiet the pass stands the cleanup Job: this operator's
// image in the cleanup role on the library's own claim.
func TestDepartStandsTheCleanupJob(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)

	if err := testOperator(t, cluster).depart(t.Context(), library, standingCatalog(), nil); err != nil {
		t.Fatal(err)
	}

	job := cluster.heldJob("house", "movies-cleanup")
	if job == nil {
		t.Fatal("the pass stood no cleanup job")
	}
	container := job.Spec.Template.Spec.Containers[0]
	if container.Command[1] != cleanupMode {
		t.Errorf("command = %v, want the cleanup role", container.Command)
	}
	if len(job.Spec.Template.Spec.Volumes) != 1 {
		t.Errorf("volumes = %+v, want the catalog claim alone", job.Spec.Template.Spec.Volumes)
	}
	if stage := departingCondition(t, cluster); stage.Reason != reasonSweeping {
		t.Errorf("Departing = %+v, want %s", stage, reasonSweeping)
	}
}

// The cleanup container carries the broker address and the topic base
// the operator was given, because the report it waits for arrives on
// the bus.
func TestTheCleanupJobCarriesTheBus(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)

	if err := testOperator(t, cluster).depart(t.Context(), library, standingCatalog(), nil); err != nil {
		t.Fatal(err)
	}

	job := cluster.heldJob("house", "movies-cleanup")
	if job == nil {
		t.Fatal("the pass stood no cleanup job")
	}
	environment := containerEnvironment(job.Spec.Template.Spec.Containers[0])
	if got := environment[busAddressVariable]; got != testBusAddress {
		t.Errorf("%s = %q, want %q", busAddressVariable, got, testBusAddress)
	}
	if got := environment[topicBaseVariable]; got != defaultTopicBase {
		t.Errorf("%s = %q, want %q", topicBaseVariable, got, defaultTopicBase)
	}
}

// a Job that exited zero is not the end of it: the rows leave the
// library's own claim only when the standing catalog holds them, and
// the reporter's echo is what says so.
func TestDepartWaitsForTheReportersEcho(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	jobs := []Job{houseJob("movies-cleanup", workerCleanup, JobStatus{Succeeded: 1})}

	if err := testOperator(t, cluster).depart(t.Context(), library, standingCatalog(), jobs); err != nil {
		t.Fatal(err)
	}

	if cluster.heldLibrary("movies") == nil {
		t.Fatal("the finalizer was released before the reporter echoed the sweep")
	}
	if stage := departingCondition(t, cluster); stage.Reason != reasonAwaitingEcho {
		t.Errorf("Departing = %+v, want %s", stage, reasonAwaitingEcho)
	}
}

// a run that names another Job, or names this one with no time on it,
// is not this sweep's echo.
func TestDepartReadsOnlyItsOwnEcho(t *testing.T) {
	cases := []struct {
		name string
		runs []libraryRun
	}{
		{name: "no runs at all"},
		{name: "another job", runs: []libraryRun{
			{Worker: workerCleanup, Job: "movies-cleanup-before", Finished: testNow},
		}},
		{name: "another worker", runs: []libraryRun{
			{Worker: workerScan, Job: "movies-cleanup", Finished: testNow},
		}},
		{name: "unfinished", runs: []libraryRun{
			{Worker: workerCleanup, Job: "movies-cleanup", Started: testNow},
		}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := departingMovies(cluster)
			operator := testOperator(t, cluster)
			operator.reports.fold("house", "movies", libraryReport{Runs: one.runs})
			jobs := []Job{houseJob("movies-cleanup", workerCleanup, JobStatus{Succeeded: 1})}

			if err := operator.depart(t.Context(), library, standingCatalog(), jobs); err != nil {
				t.Fatal(err)
			}

			if cluster.heldLibrary("movies") == nil {
				t.Error("the finalizer was released on an echo that names another run")
			}
		})
	}
}

// a failed cleanup Job blocks the release and names the failure, and it
// is deleted so the next one can mount the ReadWriteOnce claim.
func TestDepartRecreatesACleanupJobThatFailed(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	failed := houseJob("movies-cleanup", workerCleanup, JobStatus{Failed: 3})
	cluster.holdJob(&failed)
	operator := testOperator(t, cluster)

	if err := operator.depart(t.Context(), library, standingCatalog(), []Job{failed}); err != nil {
		t.Fatal(err)
	}

	if cluster.heldJob("house", "movies-cleanup") != nil {
		t.Error("the failed cleanup job stands, so no replacement can mount the claim")
	}
	stage := departingCondition(t, cluster)
	if stage.Reason != reasonBlocked {
		t.Fatalf("Departing = %+v, want %s", stage, reasonBlocked)
	}
	if !strings.Contains(stage.Message, "3 attempts") {
		t.Errorf("message = %q, want the count of attempts", stage.Message)
	}

	// the next pass finds no Job and stands one again.
	if err := operator.depart(t.Context(), library, standingCatalog(), nil); err != nil {
		t.Fatal(err)
	}
	if cluster.heldJob("house", "movies-cleanup") == nil {
		t.Error("the pass after the failure stood no cleanup job")
	}
}

// a departing Library without this operator's finalizer is not its to
// hold, so the pass writes nothing.
func TestDepartLeavesALibraryItDoesNotHold(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	library.Metadata.Finalizers = nil

	if err := testOperator(t, cluster).depart(t.Context(), library, standingCatalog(), nil); err != nil {
		t.Fatal(err)
	}

	if got := cluster.countRequests(http.MethodPatch, "libraries"); got != 0 {
		t.Errorf("patches = %d, want none", got)
	}
	if cluster.heldJob("house", "movies-cleanup") != nil {
		t.Error("a cleanup job stands for a Library this operator does not hold")
	}
}

// a read failure is reported and nothing is written, so no finalizer is
// released on unread state.
func TestDepartReportsAFailureItCannotReadPast(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{name: "the catalog claim", path: "/api/v1/namespaces/house/persistentvolumeclaims/movies-catalog"},
		{name: "the scan schedule", path: "/apis/batch/v1/namespaces/house/cronjobs/movies-scan"},
		{name: "the cleanup job", path: "/apis/batch/v1/namespaces/house/jobs"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := departingMovies(cluster)
			cluster.cronJobs["house/movies-scan"] = &CronJob{
				Metadata: ObjectMeta{Name: "movies-scan", Namespace: "house"},
			}
			cluster.broken[one.path] = http.StatusInternalServerError

			err := testOperator(t, cluster).depart(t.Context(), library, standingCatalog(), nil)

			if err == nil {
				t.Fatal("err = nil, want the failure the departure could not read past")
			}
			if cluster.heldLibrary("movies") == nil {
				t.Error("the finalizer was released on a state the pass could not read")
			}
		})
	}
}

// a cleanup Job another writer created first is success: report the
// sweep, leave the finalizer.
func TestDepartAnswersACleanupJobAnotherWriterCreated(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	cluster.refuseCreate = true

	if err := testOperator(t, cluster).depart(t.Context(), library, standingCatalog(), nil); err != nil {
		t.Fatal(err)
	}

	if stage := departingCondition(t, cluster); stage.Reason != reasonSweeping {
		t.Errorf("Departing = %+v, want %s", stage, reasonSweeping)
	}
}

// a cleanup Job already deleting counts as present; wait for the
// ReadWriteOnce claim, do not replace it.
func TestDepartWaitsForACleanupJobThatIsStillGoing(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	going := houseJob("movies-cleanup", workerCleanup, JobStatus{Failed: 1})
	going.Metadata.DeletionTimestamp = "2026-08-31T12:00:02Z"

	if err := testOperator(t, cluster).depart(t.Context(), library, standingCatalog(), []Job{going}); err != nil {
		t.Fatal(err)
	}

	if got := cluster.countRequests(http.MethodPost, "jobs"); got != 0 {
		t.Errorf("job creates = %d, want none while the job is already going", got)
	}
}

// while the backoff holds, the condition still reads Sweeping, never a
// blocker the cluster did not name.
func TestDepartReportsTheSweepWhileTheBackoffHolds(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	operator := testOperator(t, cluster)
	operator.cleanupStands["house/movies"] = cleanupStand{count: 4, next: time.Now().Add(time.Hour)}

	if err := operator.depart(t.Context(), library, standingCatalog(), nil); err != nil {
		t.Fatal(err)
	}

	if cluster.heldJob("house", "movies-cleanup") != nil {
		t.Error("a cleanup job stood while the backoff held")
	}
	if stage := departingCondition(t, cluster); stage.Reason != reasonSweeping {
		t.Errorf("Departing = %+v, want %s", stage, reasonSweeping)
	}
}

// the backoff grows to the cap and stops there, so the pass keeps
// trying for as long as the Library is deleting.
func TestTheCleanupBackoffGrowsToTheCap(t *testing.T) {
	cases := []struct {
		count int
		want  time.Duration
	}{
		{count: 1, want: cleanupBackoffBase},
		{count: 3, want: 4 * cleanupBackoffBase},
		{count: 20, want: cleanupBackoffCap},
	}
	for _, one := range cases {
		if got := cleanupBackoffDelay(one.count); got != one.want {
			t.Errorf("delay after %d stands = %v, want %v", one.count, got, one.want)
		}
	}
}

// the blocker names the failure in the cluster's own counts, and a Job
// that has not failed blocks nothing.
func TestTheCleanupBlockerNamesTheFailedJob(t *testing.T) {
	failed := houseJob("movies-cleanup", workerCleanup, JobStatus{Failed: 2})
	running := houseJob("movies-cleanup", workerCleanup, JobStatus{Active: 1})
	done := houseJob("movies-cleanup", workerCleanup, JobStatus{Succeeded: 1, Failed: 1})

	cases := []struct {
		name string
		job  *Job
		want string
	}{
		{name: "no job yet", job: nil, want: ""},
		{name: "a running job", job: &running, want: ""},
		{name: "a job that succeeded after a retry", job: &done, want: ""},
		{name: "a job that gave up", job: &failed, want: "movies-cleanup failed after 2 attempts"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got := cleanupBlocker(one.job)
			if one.want == "" {
				if got != "" {
					t.Errorf("blocker = %q, want none", got)
				}
				return
			}
			if !strings.Contains(got, one.want) {
				t.Errorf("blocker = %q, want %q in it", got, one.want)
			}
		})
	}
}
