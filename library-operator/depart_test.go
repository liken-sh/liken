package main

// what these tests read: one departure pass against a real-shaped API
// server, rung by rung.

import (
	"net/http"
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

// the second house Library, whose catalog holds a copy of the departing
// library's rows.
const theSurvivor = "shows"

// seeds the survivor with its scanner online or offline and its report;
// nil report means it said nothing.
func seedSurvivor(cluster *fakeCluster, operator *operator, online bool, report *libraryReport) {
	cluster.libraries[theSurvivor] = &Library{
		Metadata: ObjectMeta{Name: theSurvivor, Namespace: "house", UID: "shows-uid", ResourceVersion: "1"},
		Spec:     LibrarySpec{Storage: LibraryStorage{Claim: "shows"}, Kind: libraryKindSeries},
	}
	operator.reports.availability("house", theSurvivor, online)
	if report != nil {
		operator.reports.fold("house", theSurvivor, *report)
	}
}

// the report of a scanner whose catalog holds these libraries.
func holding(libraries ...string) *libraryReport {
	return &libraryReport{CatalogLibraries: libraries}
}

// the survivor list a pass over the house namespace gives the departure.
func houseSurvivors() []string { return []string{theSurvivor} }

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

	if err := testOperator(t, cluster).reconcile(t.Context(), library, withCatalog()); err != nil {
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

// a finalizer already held is not patched again, so the pass writes
// nothing on a settled object.
func TestReconcileDoesNotPatchAFinalizerItAlreadyHolds(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	library.Metadata.Finalizers = []string{libraryFinalizer}

	if err := testOperator(t, cluster).reconcile(t.Context(), library, withCatalog()); err != nil {
		t.Fatal(err)
	}

	if got := cluster.countRequests(http.MethodPatch, "libraries"); got != 0 {
		t.Errorf("patches = %d, want none", got)
	}
}

// the last Library in a namespace releases at once, because no surviving
// copy holds its rows. A deleting namespace ends the same way.
func TestDepartReleasesTheLastLibraryAtOnce(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)

	if err := testOperator(t, cluster).depart(t.Context(), library, nil); err != nil {
		t.Fatal(err)
	}

	if cluster.heldLibrary("movies") != nil {
		t.Error("the Library still stands, so the finalizer was not released")
	}
	if cluster.heldPod("movies-cleanup") != nil {
		t.Error("a cleanup pod stands for a library with nothing to clean")
	}
}

// no catalog claim means no agent ever stood, so there are no rows and the
// release is at once.
func TestDepartReleasesALibraryThatWroteNoRows(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	delete(cluster.claims, "movies-catalog")
	operator := testOperator(t, cluster)
	seedSurvivor(cluster, operator, true, holding("house/movies"))

	if err := operator.depart(t.Context(), library, houseSurvivors()); err != nil {
		t.Fatal(err)
	}

	if cluster.heldLibrary("movies") != nil {
		t.Error("the Library still stands, so the finalizer was not released")
	}
	if cluster.heldPod("movies-cleanup") != nil {
		t.Error("a cleanup pod stands for a library that wrote no rows")
	}
}

// the scanner goes first because it rewrites the rows and holds the
// ReadWriteOnce catalog claim.
func TestDepartStopsTheScannerBeforeItSweeps(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	cluster.pods["movies-scanner"] = standingPod(t, library, podRunning, true)
	operator := testOperator(t, cluster)
	seedSurvivor(cluster, operator, true, holding("house/movies"))

	if err := operator.depart(t.Context(), library, houseSurvivors()); err != nil {
		t.Fatal(err)
	}

	if cluster.heldPod("movies-scanner") != nil {
		t.Error("the scanner pod stands during a departure")
	}
	if cluster.heldPod("movies-cleanup") != nil {
		t.Error("the cleanup pod stood before the scanner was gone")
	}
	if stage := departingCondition(t, cluster); stage.Reason != reasonStoppingScanner {
		t.Errorf("Departing = %+v, want %s", stage, reasonStoppingScanner)
	}
	if phase := cluster.heldLibrary("movies").Status.Phase; phase != phaseDeparting {
		t.Errorf("phase = %q, want %s", phase, phaseDeparting)
	}
}

// a scanner pod already deleting counts as present, so the pass waits
// instead of deleting twice.
func TestDepartWaitsForAScannerPodThatIsStillGoing(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	scanner := standingPod(t, library, podRunning, true)
	scanner.Metadata.DeletionTimestamp = "2026-08-31T12:00:01Z"
	cluster.pods["movies-scanner"] = scanner
	operator := testOperator(t, cluster)
	seedSurvivor(cluster, operator, true, holding("house/movies"))

	if err := operator.depart(t.Context(), library, houseSurvivors()); err != nil {
		t.Fatal(err)
	}

	if got := cluster.countRequests(http.MethodDelete, "pods"); got != 0 {
		t.Errorf("pod deletes = %d, want none while the pod is already going", got)
	}
	if stage := departingCondition(t, cluster); stage.Reason != reasonStoppingScanner {
		t.Errorf("Departing = %+v, want %s", stage, reasonStoppingScanner)
	}
}

// scanner gone, the pass stands the cleanup pod: this operator's image in
// the cleanup role on its own claim.
func TestDepartStandsTheCleanupPod(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	operator := testOperator(t, cluster)
	seedSurvivor(cluster, operator, true, holding("house/movies"))

	if err := operator.depart(t.Context(), library, houseSurvivors()); err != nil {
		t.Fatal(err)
	}

	pod := cluster.heldPod("movies-cleanup")
	if pod == nil {
		t.Fatal("the pass stood no cleanup pod")
	}
	if pod.Spec.Containers[0].Command[1] != cleanupMode {
		t.Errorf("command = %v, want the cleanup role", pod.Spec.Containers[0].Command)
	}
	if stage := departingCondition(t, cluster); stage.Reason != reasonSweeping {
		t.Errorf("Departing = %+v, want %s", stage, reasonSweeping)
	}
}

// a failed cleanup pod is deleted so the next one can mount the
// ReadWriteOnce claim; the condition names why.
func TestDepartRecreatesACleanupPodThatFailed(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	cluster.pods["movies-cleanup"] = &Pod{
		Metadata: ObjectMeta{Name: "movies-cleanup", Namespace: "house"},
		Status:   PodStatus{Phase: podFailed, Message: "the container could not reach its catalog agent"},
	}
	operator := testOperator(t, cluster)
	seedSurvivor(cluster, operator, true, holding("house/movies"))

	if err := operator.depart(t.Context(), library, houseSurvivors()); err != nil {
		t.Fatal(err)
	}
	if cluster.heldPod("movies-cleanup") != nil {
		t.Error("the failed cleanup pod stands, so no replacement can mount the claim")
	}
	stage := departingCondition(t, cluster)
	if stage.Reason != reasonBlocked {
		t.Fatalf("Departing = %+v, want %s", stage, reasonBlocked)
	}
	if !strings.Contains(stage.Message, "could not reach its catalog agent") {
		t.Errorf("message = %q, want the kubelet's own words", stage.Message)
	}

	// the next pass finds no pod and stands one again.
	if err := operator.depart(t.Context(), library, houseSurvivors()); err != nil {
		t.Fatal(err)
	}
	if cluster.heldPod("movies-cleanup") == nil {
		t.Error("the pass after the failure stood no cleanup pod")
	}
}

// an unschedulable cleanup pod reports the scheduler's own sentence, the
// part a person acts on.
func TestDepartReportsACleanupPodThatCannotSchedule(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	cluster.pods["movies-cleanup"] = &Pod{
		Metadata: ObjectMeta{Name: "movies-cleanup", Namespace: "house"},
		Status: PodStatus{Phase: podPending, Conditions: []PodCondition{{
			Type: podScheduled, Status: conditionIsFalse, Reason: "Unschedulable",
			Message: "0/3 nodes are available: 3 node(s) had volume node affinity conflict",
		}}},
	}
	operator := testOperator(t, cluster)
	seedSurvivor(cluster, operator, true, holding("house/movies"))

	if err := operator.depart(t.Context(), library, houseSurvivors()); err != nil {
		t.Fatal(err)
	}

	stage := departingCondition(t, cluster)
	if stage.Reason != reasonBlocked {
		t.Fatalf("Departing = %+v, want %s", stage, reasonBlocked)
	}
	if !strings.Contains(stage.Message, "volume node affinity conflict") {
		t.Errorf("message = %q, want the scheduler's own words", stage.Message)
	}
}

// a departing Library without this operator's finalizer is not its to
// hold, so the pass writes nothing.
func TestDepartLeavesALibraryItDoesNotHold(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	library.Metadata.Finalizers = nil
	operator := testOperator(t, cluster)
	seedSurvivor(cluster, operator, true, holding("house/movies"))

	if err := operator.depart(t.Context(), library, houseSurvivors()); err != nil {
		t.Fatal(err)
	}

	if got := cluster.countRequests(http.MethodPatch, "libraries"); got != 0 {
		t.Errorf("patches = %d, want none", got)
	}
	if cluster.heldPod("movies-cleanup") != nil {
		t.Error("a cleanup pod stands for a Library this operator does not hold")
	}
}

// survivors are sorted per namespace and leave out deleting Libraries,
// whose own copies are leaving too.
func TestSurvivingLibrariesLeavesOutTheDeparting(t *testing.T) {
	libraries := []Library{
		{Metadata: ObjectMeta{Name: "shows", Namespace: "house"}},
		{Metadata: ObjectMeta{Name: "movies", Namespace: "house", DeletionTimestamp: "2026-08-31T12:00:00Z"}},
		{Metadata: ObjectMeta{Name: "archive", Namespace: "house"}},
		{Metadata: ObjectMeta{Name: "films", Namespace: "studio"}},
	}

	survivors := survivingLibraries(libraries)

	if want := []string{"archive", "shows"}; !equalStrings(survivors["house"], want) {
		t.Errorf("house = %v, want %v", survivors["house"], want)
	}
	if want := []string{"films"}; !equalStrings(survivors["studio"], want) {
		t.Errorf("studio = %v, want %v", survivors["studio"], want)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// a read failure is reported and nothing is written, so no finalizer is
// released on unread state.
func TestDepartReportsAFailureItCannotReadPast(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{name: "the catalog claim", path: "/api/v1/namespaces/house/persistentvolumeclaims/movies-catalog"},
		{name: "the scanner pod", path: "/api/v1/namespaces/house/pods/movies-scanner"},
		{name: "the cleanup pod", path: "/api/v1/namespaces/house/pods/movies-cleanup"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := departingMovies(cluster)
			operator := testOperator(t, cluster)
			seedSurvivor(cluster, operator, true, holding("house/movies"))
			cluster.broken[one.path] = http.StatusInternalServerError

			err := operator.depart(t.Context(), library, houseSurvivors())

			if err == nil {
				t.Fatal("err = nil, want the failure the departure could not read past")
			}
			if cluster.heldLibrary("movies") == nil {
				t.Error("the finalizer was released on a state the pass could not read")
			}
		})
	}
}

// a cleanup pod another writer created first is success: report the sweep,
// leave the finalizer.
func TestDepartAnswersACleanupPodAnotherWriterCreated(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	cluster.refuseCreate = true
	operator := testOperator(t, cluster)
	seedSurvivor(cluster, operator, true, holding("house/movies"))

	if err := operator.depart(t.Context(), library, houseSurvivors()); err != nil {
		t.Fatal(err)
	}

	if stage := departingCondition(t, cluster); stage.Reason != reasonSweeping {
		t.Errorf("Departing = %+v, want %s", stage, reasonSweeping)
	}
}

// a cleanup pod already deleting counts as present; wait for the
// ReadWriteOnce claim, do not replace it.
func TestDepartWaitsForACleanupPodThatIsStillGoing(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	pod := readyCleanupPod()
	pod.Metadata.DeletionTimestamp = "2026-08-31T12:00:02Z"
	cluster.pods["movies-cleanup"] = pod
	operator := testOperator(t, cluster)
	seedSurvivor(cluster, operator, true, holding("house/movies"))

	if err := operator.depart(t.Context(), library, houseSurvivors()); err != nil {
		t.Fatal(err)
	}

	if got := cluster.countRequests(http.MethodPost, "pods"); got != 0 {
		t.Errorf("pod creates = %d, want none while the pod is already going", got)
	}
}

// while the backoff holds, the condition still reads Sweeping, never a
// blocker the cluster did not name.
func TestDepartReportsTheSweepWhileTheBackoffHolds(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	operator := testOperator(t, cluster)
	seedSurvivor(cluster, operator, true, holding("house/movies"))
	operator.cleanupStands["house/movies"] = cleanupStand{count: 4, next: time.Now().Add(time.Hour)}

	if err := operator.depart(t.Context(), library, houseSurvivors()); err != nil {
		t.Fatal(err)
	}

	if cluster.heldPod("movies-cleanup") != nil {
		t.Error("a cleanup pod stood while the backoff held")
	}
	if stage := departingCondition(t, cluster); stage.Reason != reasonSweeping {
		t.Errorf("Departing = %+v, want %s", stage, reasonSweeping)
	}
}

// the blocker falls back from the kubelet's message to its reason to the
// bare fact of the failure.
func TestTheCleanupBlockerNamesWhatStoppedThePod(t *testing.T) {
	cases := []struct {
		name string
		pod  *Pod
		want string
	}{
		{name: "no pod yet", pod: nil, want: ""},
		{name: "a running pod", pod: readyCleanupPod(), want: ""},
		{
			name: "a failure with a message",
			pod:  &Pod{Status: PodStatus{Phase: podFailed, Message: "the agent never answered"}},
			want: "the agent never answered",
		},
		{
			name: "a failure with a reason alone",
			pod:  &Pod{Status: PodStatus{Phase: podFailed, Reason: "Evicted"}},
			want: "Evicted",
		},
		{
			name: "a failure the kubelet did not explain",
			pod:  &Pod{Status: PodStatus{Phase: podFailed}},
			want: "the kubelet gave no reason",
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got := cleanupBlocker(one.pod)
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
