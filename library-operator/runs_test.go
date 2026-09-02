package main

// What these tests prove: the runs table against the shipped
// schema, the run stream a reporter follows, and the echo a Job waits for
// on the bus.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// The wait a Job gives the echo, read out of the environment, with
// the default for every value it cannot use.
func TestEchoTimeout(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{name: "an empty value is the default", raw: "", want: defaultEchoTimeout},
		{name: "a duration", raw: "45s", want: 45 * time.Second},
		{name: "an unreadable value is the default", raw: "soon", want: defaultEchoTimeout},
		{name: "a negative value is the default", raw: "-1m", want: defaultEchoTimeout},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := echoTimeout(testCase.raw); got != testCase.want {
				t.Errorf("echoTimeout(%q) = %s, want %s", testCase.raw, got, testCase.want)
			}
		})
	}
}

// A run written twice is one row, so the finished write replaces
// the started one and every library's runs read back sorted by worker.
func TestUpsertRunAgainstTheRealSchema(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	ctx := t.Context()
	started := time.Unix(1_700_000_000, 0).UTC()
	finished := started.Add(time.Minute)

	if err := catalog.UpsertRun(ctx, "house/movies",
		libraryRun{Worker: workerScan, Job: "scan-1", Started: started}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.UpsertRun(ctx, "house/movies", libraryRun{
		Worker: workerScan, Job: "scan-1", Started: started, Finished: finished,
		Unidentified: 3, Removed: 7,
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.UpsertRun(ctx, "house/movies",
		libraryRun{Worker: workerCleanup, Job: "cleanup-1", Started: started}); err != nil {
		t.Fatal(err)
	}

	runs, err := catalog.Runs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	held := runs["house/movies"]
	if len(held) != 2 {
		t.Fatalf("runs = %+v, want the scan and the cleanup", held)
	}
	if held[0].Worker != workerCleanup || held[1].Worker != workerScan {
		t.Errorf("runs = %+v, want them sorted by worker", held)
	}
	want := libraryRun{
		Worker: workerScan, Job: "scan-1", Started: started, Finished: finished,
		Unidentified: 3, Removed: 7,
	}
	if held[1] != want {
		t.Errorf("scan run = %+v, want %+v", held[1], want)
	}
}

// A run that has not finished carries no finish time, so the
// reporter reads a walk that is still running.
func TestARunningRunHasNoFinishTime(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	started := time.Unix(1_700_000_000, 0).UTC()

	if err := catalog.UpsertRun(t.Context(), "house/movies",
		libraryRun{Worker: workerScan, Job: "scan-1", Started: started}); err != nil {
		t.Fatal(err)
	}

	runs, err := catalog.Runs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	run := runs["house/movies"][0]
	if !run.Started.Equal(started) {
		t.Errorf("started = %v, want %v", run.Started, started)
	}
	if !run.Finished.IsZero() {
		t.Errorf("finished = %v, want no finish time", run.Finished)
	}
}

// The delete takes every worker's row for one library and leaves
// every other library's runs where they are.
func TestDeleteRunsTakesOneLibrary(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	ctx := t.Context()
	for _, library := range []string{"house/movies", "house/series"} {
		for _, worker := range []string{workerScan, workerCleanup} {
			if err := catalog.UpsertRun(ctx, library,
				libraryRun{Worker: worker, Job: "job-1", Started: time.Unix(10, 0)}); err != nil {
				t.Fatal(err)
			}
		}
	}

	removed, err := catalog.DeleteRuns(ctx, "house/movies")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want the two runs of the departed library", removed)
	}

	runs, err := catalog.Runs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs["house/movies"]) != 0 {
		t.Errorf("the departed library holds %+v, want no runs", runs["house/movies"])
	}
	if len(runs["house/series"]) != 2 {
		t.Errorf("the surviving library holds %+v, want both runs", runs["house/series"])
	}
}

// A stream of the shapes a subscription sends: the columns, the
// rows of the opening snapshot, the end of the snapshot, and the changes
// after it.
func subscriptionServer(t *testing.T, status int, body string) *Catalog {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return NewCatalog(server.URL, server.Client())
}

// Every row of the snapshot and every change after it names its
// library, read by the column name because the agent's matcher prepends
// the primary key to the projection.
func TestSubscribeRunsNamesTheLibraryOfEveryRowAndChange(t *testing.T) {
	body := `{"columns":["library","worker","job","started","finished","unidentified","removed"]}` + "\n" +
		`{"row":[1,["house/movies","scan","scan-1",10,20,0,0]]}` + "\n" +
		`{"eoq":{"time":0.1,"change_id":4}}` + "\n" +
		`{"change":["update",1,["house/series","scan","scan-2",30,40,1,2],5]}` + "\n" +
		`{"change":["delete",2,["house/departed","cleanup","cleanup-1",30,40,0,0],6]}` + "\n"
	catalog := subscriptionServer(t, http.StatusOK, body)

	var named []string
	ready := 0
	if err := catalog.subscribeRuns(t.Context(), func() { ready++ }, func(library string) {
		named = append(named, library)
	}); err != nil {
		t.Fatal(err)
	}

	want := []string{"house/movies", "house/series", "house/departed"}
	if !slices.Equal(named, want) {
		t.Errorf("named = %v, want %v", named, want)
	}
	if ready != 1 {
		t.Errorf("the snapshot ended %d times, want once", ready)
	}
}

// The matcher prepends the primary key to the projection, so a
// reader that counted cells would read the wrong column.
func TestSubscribeRunsReadsTheLibraryPastPrependedKeyColumns(t *testing.T) {
	body := `{"columns":["worker","library","job","started","finished","unidentified","removed"]}` + "\n" +
		`{"change":["insert",1,["scan","house/movies","scan-1",10,20,0,0],5]}` + "\n"
	catalog := subscriptionServer(t, http.StatusOK, body)

	var named []string
	if err := catalog.subscribeRuns(t.Context(), func() {}, func(library string) {
		named = append(named, library)
	}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"house/movies"}; !slices.Equal(named, want) {
		t.Errorf("named = %v, want %v", named, want)
	}
}

// A stream that carries no library, or that ends in an error or a
// refusal, is an error the caller answers with a fresh stream.
func TestSubscribeReadsWhatItCanAndSurfacesTheRest(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		named   []string
		wantErr bool
	}{
		{
			name:   "a row with no library column",
			status: http.StatusOK,
			body:   `{"columns":["worker"]}` + "\n" + `{"row":[1,["scan"]]}` + "\n",
		},
		{
			name:   "a row shorter than its columns",
			status: http.StatusOK,
			body:   `{"columns":["library","worker"]}` + "\n" + `{"row":[1,[]]}` + "\n",
		},
		{
			name:   "a row that is not a pair",
			status: http.StatusOK,
			body:   `{"columns":["library"]}` + "\n" + `{"row":[1]}` + "\n",
		},
		{
			name:   "an event of no kind the reader acts on",
			status: http.StatusOK,
			body:   `{"notify":["update",[1]]}` + "\n",
		},
		{
			name:    "an error event",
			status:  http.StatusOK,
			body:    `{"error":"no such table: runs"}` + "\n",
			wantErr: true,
		},
		{
			name:    "a line that is not JSON",
			status:  http.StatusOK,
			body:    "not json\n",
			wantErr: true,
		},
		{
			name:    "columns that are not a list",
			status:  http.StatusOK,
			body:    `{"columns":"library"}` + "\n",
			wantErr: true,
		},
		{
			name:    "cells that are not a list",
			status:  http.StatusOK,
			body:    `{"columns":["library"]}` + "\n" + `{"row":[1,"house/movies"]}` + "\n",
			wantErr: true,
		},
		{
			name:    "a refused subscription",
			status:  http.StatusInternalServerError,
			body:    "the agent is unwell",
			wantErr: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			catalog := subscriptionServer(t, testCase.status, testCase.body)
			var named []string
			err := catalog.subscribeRuns(t.Context(), func() {}, func(library string) {
				named = append(named, library)
			})
			if (err != nil) != testCase.wantErr {
				t.Errorf("error = %v, want an error: %v", err, testCase.wantErr)
			}
			if !slices.Equal(named, testCase.named) {
				t.Errorf("named = %v, want %v", named, testCase.named)
			}
		})
	}
}

// A Job's wait, wired to the test broker, so the subscription and
// the report that ends it travel a real connection.
func waitingJob(t *testing.T, worker, job string) (*echoWaiter, <-chan *fakeBroker, *Bus) {
	t.Helper()
	address, accepted := testBroker(t)
	shorterBackoff(t)
	wait := newEchoWaiter(libraryStatusTopic(defaultTopicBase, "house", "movies"), worker, job)
	bus := newBus(address, "scan-house-movies", nil, nil, wait.note)
	return wait, accepted, bus
}

// A report that names this Job's finished run ends the wait, which
// is what lets the Job exit knowing the standing pod holds its rows.
func TestTheEchoEndsTheWait(t *testing.T) {
	wait, accepted, bus := waitingJob(t, workerScan, "scan-1")
	done := make(chan error, 1)
	go func() { done <- wait.wait(t.Context(), bus, scanTestTimeout) }()

	broker := waitForBroker(t, accepted)
	if got := waitForString(t, broker.subs); got != wait.topic {
		t.Fatalf("the Job subscribed to %q, want %q", got, wait.topic)
	}
	broker.push(wait.topic, reportOf(t, libraryRun{
		Worker: workerScan, Job: "scan-1",
		Started: time.Unix(10, 0), Finished: time.Unix(20, 0),
	}))

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the wait ended with %v, want the echo", err)
		}
	case <-time.After(scanTestTimeout):
		t.Fatal("the echo never ended the wait")
	}
}

// A report that names no run of this Job leaves the wait standing,
// so a Job never reads another worker's echo as its own.
func TestTheWaitStandsOnAReportThatIsNotItsOwn(t *testing.T) {
	cases := []struct {
		name    string
		topic   string
		payload []byte
	}{
		{
			name:    "another worker's run",
			payload: reportOf(t, libraryRun{Worker: workerCleanup, Job: "scan-1", Finished: time.Unix(20, 0)}),
		},
		{
			name:    "another Job's run",
			payload: reportOf(t, libraryRun{Worker: workerScan, Job: "scan-0", Finished: time.Unix(20, 0)}),
		},
		{
			name:    "a run that has not finished",
			payload: reportOf(t, libraryRun{Worker: workerScan, Job: "scan-1", Started: time.Unix(10, 0)}),
		},
		{
			name:    "a report with no runs",
			payload: []byte(`{"titles":3}`),
		},
		{
			name:    "a payload that is not a report",
			payload: []byte("3 titles"),
		},
		{
			name:    "another library's topic",
			topic:   libraryStatusTopic(defaultTopicBase, "house", "series"),
			payload: reportOf(t, libraryRun{Worker: workerScan, Job: "scan-1", Finished: time.Unix(20, 0)}),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			wait := newEchoWaiter(libraryStatusTopic(defaultTopicBase, "house", "movies"), workerScan, "scan-1")
			topic := testCase.topic
			if topic == "" {
				topic = wait.topic
			}

			wait.note(topic, testCase.payload)

			select {
			case <-wait.echoed:
				t.Error("the wait ended on a report that names no run of this Job")
			default:
			}
		})
	}
}

// One whole report, as the reporter publishes it.
func mustMarshal(t *testing.T, report libraryReport) []byte {
	t.Helper()
	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// A Job that expects counts waits until the report carries them,
// because the run row reaches the standing pod before the rows the Job
// wrote ahead of it.
func TestTheWaitStandsUntilTheCountsMatch(t *testing.T) {
	cases := []struct {
		name  string
		items int
		files int
		want  bool
	}{
		{name: "the counts the Job wrote", items: 1415, files: 2830, want: true},
		{name: "fewer items than the Job wrote", items: 1149, files: 2830},
		{name: "fewer files than the Job wrote", items: 1415, files: 2000},
		{name: "an empty library", items: 0, files: 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			wait := newEchoWaiter(libraryStatusTopic(defaultTopicBase, "house", "movies"), workerScan, "scan-1")
			wait.expect(1415, 2830)

			wait.note(wait.topic, mustMarshal(t, libraryReport{
				Items: testCase.items, Files: testCase.files,
				Runs: []libraryRun{{Worker: workerScan, Job: "scan-1", Finished: time.Unix(20, 0)}},
			}))

			echoed := false
			select {
			case <-wait.echoed:
				echoed = true
			default:
			}
			if echoed != testCase.want {
				t.Errorf("the wait ended: %v, want %v", echoed, testCase.want)
			}
		})
	}
}

// A Job that could not read its counts waits on the run alone, so a
// walk that failed still holds its agent open until its rows land.
func TestAWaitWithNoCountsEndsOnTheRunAlone(t *testing.T) {
	wait := newEchoWaiter(libraryStatusTopic(defaultTopicBase, "house", "movies"), workerScan, "scan-1")

	wait.note(wait.topic, mustMarshal(t, libraryReport{
		Items: 1149, Files: 2000,
		Runs: []libraryRun{{Worker: workerScan, Job: "scan-1", Finished: time.Unix(20, 0)}},
	}))

	select {
	case <-wait.echoed:
	default:
		t.Error("the wait stands on a report that names the run, with no counts expected")
	}
}

// An echo that never arrives fails the Job, so its rows stay on its
// own claim and the retry carries them.
func TestTheWaitFailsOnItsTimeout(t *testing.T) {
	wait, _, bus := waitingJob(t, workerScan, "scan-1")

	err := wait.wait(t.Context(), bus, 10*time.Millisecond)

	if err == nil {
		t.Fatal("the wait ended with no error, want the timeout")
	}
	if !strings.Contains(err.Error(), "scan-1") {
		t.Errorf("error = %v, want the Job named", err)
	}
}

// A cancelled context ends the wait, so a Job the kubelet stops
// does not hold the pod open for the whole timeout.
func TestTheWaitEndsOnACancelledContext(t *testing.T) {
	wait, _, bus := waitingJob(t, workerScan, "scan-1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := wait.wait(ctx, bus, scanTestTimeout); err == nil {
		t.Error("the wait ended with no error, want the cancelled context")
	}
}

// One report carrying one run, as the reporter publishes it.
func reportOf(t *testing.T, runs ...libraryRun) []byte {
	t.Helper()
	payload, err := json.Marshal(libraryReport{Runs: runs})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// An agent that answers everything the catalog it fronts answers,
// except the requests the test refuses, so a test drives one failed write
// in the middle of a Job.
func proxyCatalog(t *testing.T, catalog *Catalog, refuse func(path string, body []byte) bool) *Catalog {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if refuse(r.URL.Path, body) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		answer, err := http.Post(catalog.base+r.URL.Path, "application/json", bytes.NewReader(body))
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer answer.Body.Close()
		w.WriteHeader(answer.StatusCode)
		_, _ = io.Copy(w, answer.Body)
	}))
	t.Cleanup(server.Close)
	return NewCatalog(server.URL, server.Client())
}

// A row the read cannot make a run of is skipped, so one row of a
// shape this operator did not write never costs the reporter the rest of
// the table.
func TestRunsSkipsARowItCannotRead(t *testing.T) {
	cases := []struct {
		name string
		row  string
	}{
		{name: "a row shorter than the columns", row: `{"row":[1,["house/movies","scan"]]}`},
		{name: "a library that is not a string", row: `{"row":[1,[7,"scan","scan-1",10,20,0,0]]}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			body := `{"columns":["library","worker","job","started","finished","unidentified","removed"]}` + "\n" +
				testCase.row + "\n" +
				`{"row":[2,["house/series","scan","scan-2",10,20,0,0]]}` + "\n" +
				`{"eoq":{"time":0.1}}` + "\n"
			catalog := subscriptionServer(t, http.StatusOK, body)

			runs, err := catalog.Runs(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(runs) != 1 || len(runs["house/series"]) != 1 {
				t.Errorf("runs = %+v, want the one row the read could make a run of", runs)
			}
		})
	}
}
