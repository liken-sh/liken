package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// Which work lists the pass keeps on the bus, and the clear of the rest.

// The trickplay list of the library Job named.
func trickplayListOf(run string) workList {
	list := trickplayList
	list.run = run
	return list
}

// A report whose enrich run is the library Job named, finished or not.
func reportOfRun(run string, finished bool) *libraryReport {
	listed := libraryRun{Worker: workerEnrich, Job: run, Started: testNow.Add(-time.Minute)}
	if finished {
		listed.Finished = testNow
	}
	return &libraryReport{Runs: []libraryRun{listed}}
}

// A list stays while its worker runs or while a worker can still start on
// it, and goes once it is worked or replaced.
func TestWhichListsThePassKeeps(t *testing.T) {
	cases := []struct {
		name    string
		library *Library
		jobs    []Job
		taken   string
		report  *libraryReport
		others  []string
		keep    bool
	}{
		{name: "the newest list, no worker yet", library: trickplayMovies(), keep: true},
		{name: "a Library that is gone"},
		{name: "a fact the Library turned off", library: studioMovies()},
		{name: "a list a worker works", library: trickplayMovies(),
			jobs: []Job{trickplayJob(JobStatus{Active: 4}, "movies-walk-1")}, keep: true},
		{name: "a list a finished worker worked", library: trickplayMovies(),
			jobs: []Job{trickplayJob(succeededStatus(testNow), "movies-walk-1")}},
		{name: "a list the pass started a worker on, whose Job the pass has not read", library: trickplayMovies(),
			taken: "movies-walk-1", keep: true},
		{name: "a newer list of the fact", library: trickplayMovies(), others: []string{"movies-walk-2"}},
		{name: "an older list of the fact", library: trickplayMovies(), others: []string{"movies-walk-0"},
			keep: true},
		{name: "a newer library Job that finished", library: trickplayMovies(),
			report: reportOfRun("movies-walk-2", true)},
		{name: "a newer library Job that runs", library: trickplayMovies(),
			report: reportOfRun("movies-walk-2", false), keep: true},
		{name: "the library Job that published it", library: trickplayMovies(),
			report: reportOfRun("movies-walk-1", true), keep: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			operator := testOperator(t, newFakeCluster())
			key := libraryKey("house", "movies") + "/" + factTrickplay
			if one.taken != "" {
				operator.workListsTaken[key] = one.taken
			}
			if one.report != nil {
				operator.reports.fold("house", "movies", *one.report)
			}
			list := trickplayListOf("movies-walk-1")
			held := []workList{list}
			for _, other := range one.others {
				held = append(held, trickplayListOf(other))
			}

			kept := operator.keepsWorkList(list, one.library, one.jobs, held)

			if kept != one.keep {
				t.Errorf("kept = %v, want %v", kept, one.keep)
			}
		})
	}
}

// An operator whose bus reaches the broker given, holding the list of
// movies-walk-1 that the broker retains with the three videos.
func sweepingOperator(t *testing.T, broker *retainBroker) *operator {
	t.Helper()
	operator := testOperator(t, newFakeCluster())
	operator.bus.dial = broker.dial
	list := trickplayListOf("movies-walk-1")
	if err := publishWorkList(testSession(t, broker), defaultTopicBase, list, threeVideos()); err != nil {
		t.Fatal(err)
	}
	operator.workLists.fold(list, []byte("3"))
	return operator
}

// The buffer the operator's lines go to from here on.
func captureLog(operator *operator) *bytes.Buffer {
	logged := &bytes.Buffer{}
	operator.log = logged
	return logged
}

// The pass that sees a list's worker finished clears every topic of the
// list, and forgets the list.
func TestThePassClearsTheListOfAFinishedWorker(t *testing.T) {
	broker := newRetainBroker(t)
	operator := sweepingOperator(t, broker)
	logged := captureLog(operator)
	finished := trickplayJob(succeededStatus(testNow), "movies-walk-1")

	operator.sweepWorkLists(t.Context(), []Library{*trickplayMovies()}, []Job{finished})

	if held := broker.retainedUnder(trickplayListOf("movies-walk-1").prefix(defaultTopicBase)); len(held) != 0 {
		t.Errorf("the broker still holds %v", held)
	}
	if lists := operator.workLists.held(); len(lists) != 0 {
		t.Errorf("the desk still holds %v", lists)
	}
	wantOneLine(t, logged, "library house/movies: cleared the trickplay list of 3 videos from the job movies-walk-1")
}

// The pass keeps a list a worker can still start on.
func TestThePassKeepsAListNoWorkerTook(t *testing.T) {
	broker := newRetainBroker(t)
	operator := sweepingOperator(t, broker)

	operator.sweepWorkLists(t.Context(), []Library{*trickplayMovies()}, nil)

	if held := broker.retainedUnder(trickplayListOf("movies-walk-1").prefix(defaultTopicBase)); len(held) != 4 {
		t.Errorf("the broker holds %v, want the three videos and the count", held)
	}
}

// A broker the pass cannot reach leaves the list on the desk, so the next
// pass clears it.
func TestAnUnreachableBrokerLeavesTheListForTheNextPass(t *testing.T) {
	broker := newRetainBroker(t)
	operator := sweepingOperator(t, broker)
	logged := captureLog(operator)
	operator.bus.dial = func(context.Context) (net.Conn, error) { return nil, errors.New("connection refused") }

	operator.sweepWorkLists(t.Context(), nil, nil)

	if lists := operator.workLists.held(); len(lists) != 1 {
		t.Errorf("the desk holds %v, want the list", lists)
	}
	if !strings.Contains(logged.String(), "could not clear 1 work list") {
		t.Errorf("log = %q, want the failure", logged)
	}
}

// The count of a list arrives on the bus: a new count wakes a pass, a clear
// drops the list, and a payload that is not a count changes nothing.
func TestTheDeskFoldsTheCounts(t *testing.T) {
	wake := make(chan struct{}, 1)
	desk := newWorkLists(wake)
	list := trickplayListOf("movies-walk-1")

	desk.fold(list, []byte("3"))
	if count, held := desk.countOf(list); !held || count != 3 || len(wake) != 1 {
		t.Errorf("count = %d, %v with %d wakes, want 3 and a wake", count, held, len(wake))
	}
	desk.fold(list, []byte("three"))
	if count, _ := desk.countOf(list); count != 3 {
		t.Errorf("count = %d after a payload that is not a count, want 3", count)
	}
	desk.fold(list, nil)
	if _, held := desk.countOf(list); held {
		t.Error("the desk holds a cleared list")
	}
}

// The operator folds a count that reaches its bus.
func TestTheOperatorFoldsACountFromTheBus(t *testing.T) {
	operator := testOperator(t, newFakeCluster())
	list := trickplayListOf("movies-walk-1")

	operator.handleBusMessage(list.countTopic(defaultTopicBase), []byte("12"))

	if count, held := operator.workLists.countOf(list); !held || count != 12 {
		t.Errorf("count = %d, %v, want 12", count, held)
	}
}
