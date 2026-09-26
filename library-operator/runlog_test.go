package main

// These tests read the line a run leaves when its row in a library report
// ends: once, on the report that first carries the end.

import (
	"encoding/json"
	"testing"
	"time"
)

// The report the reporter publishes for the house's movies, delivered on
// its topic the way the broker delivers it.
func publishRuns(operator *operator, runs ...libraryRun) {
	payload, err := json.Marshal(libraryReport{Runs: runs})
	if err != nil {
		panic(err)
	}
	operator.handleBusMessage(libraryStatusTopic(defaultTopicBase, "house", "movies"), payload)
}

var (
	runStarted = libraryRun{Worker: workerScan, Job: "movies-walk-1", Started: testNow}
	runEnded   = libraryRun{Worker: workerScan, Job: "movies-walk-1", Started: testNow,
		Finished: testNow.Add(time.Minute), Unidentified: 2, Removed: 1}
	runFailed = libraryRun{Worker: workerEnrich, Job: "movies-walk-1", Started: testNow,
		Finished: testNow.Add(time.Minute), Failure: "the catalog answered 503: no leader"}
)

func TestARunThatEndsLeavesOneLine(t *testing.T) {
	cases := []struct {
		name    string
		reports [][]libraryRun
		want    []string
	}{
		{
			name:    "a walk that finished",
			reports: [][]libraryRun{{runStarted}, {runEnded}},
			want:    []string{"library house/movies: the job movies-walk-1 finished its scan run", "2 unidentified", "1 removed"},
		},
		{
			name:    "a run that failed",
			reports: [][]libraryRun{{runStarted}, {runStarted, runFailed}},
			want:    []string{"the job movies-walk-1 ended its enrich run with a failure: the catalog answered 503: no leader"},
		},
		{
			name: "a finished row written again with its write",
			reports: [][]libraryRun{{runStarted}, {runEnded}, {func() libraryRun {
				again := runEnded
				again.Actor, again.Version = sqliteAgentActor, 77
				return again
			}()}},
			want: []string{"finished its scan run"},
		},
		{
			name:    "the same report published again",
			reports: [][]libraryRun{{runStarted}, {runEnded}, {runEnded}},
			want:    []string{"finished its scan run"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			operator, logged := loggingOperator(t, newFakeCluster())
			for _, runs := range c.reports {
				publishRuns(operator, runs...)
			}
			wantOneLine(t, logged, c.want...)
		})
	}
}

// The first report after a restart carries runs that ended before it, and
// the desk held nothing to compare them with, so it leaves no line.
func TestTheFirstReportLeavesNoLine(t *testing.T) {
	operator, logged := loggingOperator(t, newFakeCluster())

	publishRuns(operator, runEnded, runFailed)

	if logged.Len() != 0 {
		t.Errorf("logged %q, want nothing", logged)
	}
}
