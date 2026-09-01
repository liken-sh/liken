package main

// what these tests read: the release rule, and what the release itself
// does to the pod, the bus and the finalizer.

import (
	"net/http"
	"strings"
	"testing"
)

// every survivor holds the release: offline, still naming the library, or
// never having reported.
func TestDepartWaitsForEverySurvivor(t *testing.T) {
	cases := []struct {
		name    string
		online  bool
		report  *libraryReport
		blocker string
	}{
		{
			name:   "a survivor that is offline",
			report: holding("house/shows"), blocker: "offline",
		},
		{
			name:   "a survivor whose report still names the library",
			online: true, report: holding("house/movies", "house/shows"),
			blocker: "still holds house/movies",
		},
		{name: "a survivor that has not reported", online: true, blocker: "has not reported"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := departingMovies(cluster)
			operator := testOperator(t, cluster)
			seedSurvivor(cluster, operator, one.online, one.report)
			// the cleanup pod is up, so the ladder has reached the wait.
			cluster.pods["movies-cleanup"] = readyCleanupPod()

			if err := operator.depart(t.Context(), library, houseSurvivors(), withCatalog()); err != nil {
				t.Fatal(err)
			}

			if cluster.heldLibrary("movies") == nil {
				t.Fatal("the finalizer was released while a survivor still held the rows")
			}
			stage := departingCondition(t, cluster)
			if stage.Reason != reasonAwaitingSurvivor {
				t.Fatalf("Departing = %+v, want %s", stage, reasonAwaitingSurvivor)
			}
			if !strings.Contains(stage.Message, theSurvivor) {
				t.Errorf("message = %q, want the survivor named", stage.Message)
			}
			if !strings.Contains(stage.Message, one.blocker) {
				t.Errorf("message = %q, want %q", stage.Message, one.blocker)
			}
		})
	}
}

// the cleanup pod as the kubelet reports it with both containers up, the
// state the wait runs in.
func readyCleanupPod() *Pod {
	return &Pod{
		Metadata: ObjectMeta{Name: "movies-cleanup", Namespace: "house"},
		Status: PodStatus{
			Phase:                 podRunning,
			InitContainerStatuses: []ContainerStatus{{Name: catalogContainer, Ready: true}},
			ContainerStatuses:     []ContainerStatus{{Name: cleanupContainer, Ready: true}},
		},
	}
}

// the whole release rule: every survivor online and none naming the
// library; pod retired, finalizer off.
func TestDepartReleasesWhenEverySurvivorIsClean(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	cluster.pods["movies-cleanup"] = readyCleanupPod()
	operator := testOperator(t, cluster)
	seedSurvivor(cluster, operator, true, holding("house/shows"))

	if err := operator.depart(t.Context(), library, houseSurvivors(), withCatalog()); err != nil {
		t.Fatal(err)
	}

	if cluster.heldLibrary("movies") != nil {
		t.Error("the Library still stands, so the finalizer was not released")
	}
	if cluster.heldPod("movies-cleanup") != nil {
		t.Error("the cleanup pod stands after the release")
	}
}

// the release empties the retained report and availability, so a later
// subscriber reads nothing.
func TestDepartClearsTheRetainedTopics(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	cluster.pods["movies-cleanup"] = readyCleanupPod()
	operator, broker := operatorOnABroker(t, cluster)
	seedSurvivor(cluster, operator, true, holding("house/shows"))

	if err := operator.depart(t.Context(), library, houseSurvivors(), withCatalog()); err != nil {
		t.Fatal(err)
	}

	cleared := clearedTopics(t, broker, 2)
	for _, topic := range libraryTopics("house", "movies") {
		if !cleared[topic] {
			t.Errorf("%s was not cleared", topic)
		}
	}
}

// a conflict is read again next pass and an absent Library is the goal;
// neither is reported as a failure.
func TestReleaseCarriesOnFromAConflictAndAnAbsentLibrary(t *testing.T) {
	cases := []struct {
		name  string
		setup func(cluster *fakeCluster)
	}{
		{
			name: "a Library written since the list",
			setup: func(cluster *fakeCluster) {
				cluster.libraries["movies"].Metadata.ResourceVersion = "99"
			},
		},
		{
			name:  "a Library that is already gone",
			setup: func(cluster *fakeCluster) { delete(cluster.libraries, "movies") },
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := departingMovies(cluster)
			operator := testOperator(t, cluster)
			one.setup(cluster)

			if err := operator.releaseLibrary(t.Context(), library); err != nil {
				t.Fatalf("the release reported %v, want it to carry on", err)
			}
		})
	}
}

// a release that cannot retire the cleanup pod reports it and keeps the
// finalizer on.
func TestReleaseReportsAFailureToRetireTheCleanupPod(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	cluster.broken["/api/v1/namespaces/house/pods/movies-cleanup"] = http.StatusInternalServerError

	err := testOperator(t, cluster).releaseLibrary(t.Context(), library)

	if err == nil {
		t.Fatal("err = nil, want the failure the release could not read past")
	}
	if cluster.heldLibrary("movies") == nil {
		t.Error("the finalizer was released while the cleanup pod stands")
	}
}
