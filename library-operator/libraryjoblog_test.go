package main

// These tests read the line a library Job leaves when the pass creates it:
// what kind of run it is, and what caused it.

import (
	"testing"
	"time"
)

func TestACreatedJobLeavesOneLineWithItsCause(t *testing.T) {
	hourAgo := testNow.Add(-time.Hour)
	walked := &libraryReport{Runs: walkedRuns(testNow.Add(time.Minute))}
	cases := []struct {
		name    string
		report  *libraryReport
		held    []string
		refresh map[string]time.Time
		want    []string
	}{
		{name: "a new Library", want: []string{"a full walk", "because the library has no walk yet"}},
		{name: "an hour past the last walk", report: &libraryReport{Runs: walkedRuns(hourAgo)},
			want: []string{"a full walk", "because spec.scan.schedule came due"}},
		{name: "a request", report: walked, refresh: map[string]time.Time{refreshWalk: testNow.Add(30 * time.Second)},
			want: []string{"a full walk", "because spec.refresh asked for a walk at 2026-08-29T12:00:30Z"}},
		{name: "a webhook's folders", report: walked, held: []string{"/a", "/b"},
			want: []string{"a walk of 2 folders", "because a webhook named 2 folders"}},
		{name: "a webhook that named no folder", report: walked, held: []string{""},
			want: []string{"a full walk", "because a webhook asked for a full walk"}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			cluster := newFakeCluster()
			library := boundHouse(cluster)
			library.Spec.Trickplay.Enabled = true
			library.Spec.Refresh = one.refresh
			operator, logged := loggingOperator(t, cluster)
			for _, path := range one.held {
				operator.paths.hold("house", "movies", path)
			}

			if err := operator.runLibrary(t.Context(), library, one.report, nil, providerSet{}, testNow); err != nil {
				t.Fatal(err)
			}

			created := cluster.heldJobs()
			if len(created) != 1 {
				t.Fatalf("jobs = %+v, want one", created)
			}
			words := append([]string{"library house/movies: created the job " + created[0].Metadata.Name}, one.want...)
			wantOneLine(t, logged, words...)
			if linesWith(logged, "/a") != nil {
				t.Errorf("the log names a folder:\n%s", logged)
			}
		})
	}
}

// A refresh of a fact is the cause of the Job that fills its gap, and the
// line names the fact.
func TestARefreshOfAFactIsTheCauseOfItsJob(t *testing.T) {
	cluster := newFakeCluster()
	library, providers := libraryWithProvider()
	cluster.libraries["movies"] = library
	library.Spec.Scan.Schedule = "0 0 * * *"
	library.Spec.Refresh = map[string]time.Time{factIdentity: testNow.Add(-time.Minute)}
	report := &libraryReport{Runs: walkedRuns(testNow.Add(-2 * time.Minute)),
		OldestAttempts: map[string]time.Time{factIdentity: testNow.Add(-24 * time.Hour)}}
	operator, logged := loggingOperator(t, cluster)

	if err := operator.runLibrary(t.Context(), library, report, nil, providers, testNow); err != nil {
		t.Fatal(err)
	}

	wantOneLine(t, logged, "a job that fills gaps", "because spec.refresh asked again for identity")
}

// A pass that creates no Job adds no line.
func TestAPassThatCreatesNoJobAddsNoLine(t *testing.T) {
	cluster := newFakeCluster()
	library := boundHouse(cluster)
	operator, logged := loggingOperator(t, cluster)
	running := []Job{walkJob(JobStatus{Active: 1})}

	if err := operator.runLibrary(t.Context(), library, nil, running, providerSet{}, testNow); err != nil {
		t.Fatal(err)
	}

	if logged.Len() != 0 {
		t.Errorf("logged %q, want nothing", logged)
	}
}
