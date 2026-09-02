package main

// what these tests read: the release rule, and what the release itself
// does to the Job, the bus and the finalizer.

import (
	"net/http"
	"testing"
)

// the whole release rule: the cleanup Job exited zero and the
// namespace's reporter echoed that same Job back.
func TestDepartReleasesWhenTheSweepIsEchoed(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	done := houseJob("movies-cleanup", workerCleanup, JobStatus{Succeeded: 1})
	cluster.holdJob(&done)
	operator := testOperator(t, cluster)
	operator.reports.fold("house", "movies", echoing("movies-cleanup"))

	if err := operator.depart(t.Context(), library, standingCatalog(), []Job{done}); err != nil {
		t.Fatal(err)
	}

	if cluster.heldLibrary("movies") != nil {
		t.Error("the Library still stands, so the finalizer was not released")
	}
	if cluster.heldJob("house", "movies-cleanup") != nil {
		t.Error("the cleanup job stands after the release")
	}
}

// the release empties the retained report and availability, so a later
// subscriber reads nothing.
func TestDepartClearsTheRetainedTopics(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	done := houseJob("movies-cleanup", workerCleanup, JobStatus{Succeeded: 1})
	cluster.holdJob(&done)
	operator, broker := operatorOnABroker(t, cluster)
	operator.reports.fold("house", "movies", echoing("movies-cleanup"))

	if err := operator.depart(t.Context(), library, standingCatalog(), []Job{done}); err != nil {
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

// a release that cannot retire the cleanup Job reports it and keeps the
// finalizer on.
func TestReleaseReportsAFailureToRetireTheCleanupJob(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	done := houseJob("movies-cleanup", workerCleanup, JobStatus{Succeeded: 1})
	cluster.holdJob(&done)
	cluster.broken["/apis/batch/v1/namespaces/house/jobs/movies-cleanup"] = http.StatusInternalServerError

	err := testOperator(t, cluster).releaseLibrary(t.Context(), library)

	if err == nil {
		t.Fatal("err = nil, want the failure the release could not read past")
	}
	if cluster.heldLibrary("movies") == nil {
		t.Error("the finalizer was released while the cleanup job stands")
	}
}
