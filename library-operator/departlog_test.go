package main

// These tests read the lines a deleted Library leaves: the cleanup Job the
// departure creates, and the release of the finalizer with its reason.

import (
	"testing"
)

func TestADepartureLeavesOneLinePerStep(t *testing.T) {
	cleanup := houseJob("movies-cleanup", workerCleanup, JobStatus{Succeeded: 1})
	cases := []struct {
		name   string
		choice catalogChoice
		jobs   []Job
		want   string
	}{
		{
			name:   "the sweep starts",
			choice: standingCatalog(),
			want:   "library house/movies is deleting: created the job movies-cleanup to sweep its rows from the catalog",
		},
		{
			name:   "the sweep is confirmed",
			choice: standingCatalog(),
			jobs:   []Job{cleanup},
			want:   "library house/movies: released the finalizer, because the job movies-cleanup swept its rows and a catalog pod confirmed the sweep",
		},
		{
			name:   "no Catalog",
			choice: singleCatalog(nil),
			want:   "library house/movies: released the finalizer, because the namespace holds no Catalog",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := departingMovies(cluster)
			operator, logged := loggingOperator(t, cluster)

			if err := operator.depart(t.Context(), library, c.choice, c.jobs); err != nil {
				t.Fatal(err)
			}

			wantOneLine(t, logged, c.want)
		})
	}
}

// A sweep that runs leaves no second line on the pass after the one that
// started it.
func TestASweepThatRunsAddsNoLine(t *testing.T) {
	cluster := newFakeCluster()
	library := departingMovies(cluster)
	operator, logged := loggingOperator(t, cluster)
	running := []Job{houseJob("movies-cleanup", workerCleanup, JobStatus{Active: 1})}

	if err := operator.depart(t.Context(), library, standingCatalog(), running); err != nil {
		t.Fatal(err)
	}

	if logged.Len() != 0 {
		t.Errorf("logged %q, want nothing", logged)
	}
}
