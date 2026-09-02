package main

// What these tests run: the cleanup Job against a catalog it can
// reach and one it cannot, with no pod.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// the sweeper reads its library from the environment, because it holds no
// credential, and logs that one line.
func TestNewSweeperReadsItsLibraryFromTheEnvironment(t *testing.T) {
	t.Setenv(libraryNamespaceVariable, "house")
	t.Setenv(libraryNameVariable, "movies")
	t.Setenv(catalogAPIVariable, "")
	t.Setenv(topicBaseVariable, "")
	t.Setenv(jobNameVariable, "movies-cleanup-1")
	t.Setenv(echoTimeoutVariable, "90s")
	var logged bytes.Buffer

	sweep := newSweeper(&logged)

	if sweep.library != "house/movies" {
		t.Errorf("library = %q, want house/movies", sweep.library)
	}
	if sweep.catalog.base != defaultCatalogAPI {
		t.Errorf("catalog = %q, want the loopback agent %s", sweep.catalog.base, defaultCatalogAPI)
	}
	if !strings.Contains(logged.String(), "house/movies") {
		t.Errorf("log = %q, want the library it sweeps", logged.String())
	}
	if sweep.job != "movies-cleanup-1" || sweep.echoTimeout != 90*time.Second {
		t.Errorf("job = %q with a %s wait, want the Job and the wait the environment names",
			sweep.job, sweep.echoTimeout)
	}
	if sweep.echo.worker != workerCleanup || sweep.echo.topic != libraryStatusTopic(defaultTopicBase, "house", "movies") {
		t.Errorf("the echo waits on %s of %s, want this library's cleanup run",
			sweep.echo.worker, sweep.echo.topic)
	}
}

// a catalog address in the environment is the one it posts to, which is
// how a test drives the role.
func TestNewSweeperTakesTheCatalogAddressItIsGiven(t *testing.T) {
	t.Setenv(libraryNamespaceVariable, "house")
	t.Setenv(libraryNameVariable, "movies")
	t.Setenv(catalogAPIVariable, "http://127.0.0.1:9999")

	sweep := newSweeper(&bytes.Buffer{})

	if sweep.catalog.base != "http://127.0.0.1:9999" {
		t.Errorf("catalog = %q, want the address the environment named", sweep.catalog.base)
	}
}

// One cleanup Job over a catalog loaded with the shipped schema,
// wired to the test broker, so the sweep and the echo run with no pod.
func cleanupJob(t *testing.T, catalog *Catalog) (*sweeper, <-chan *fakeBroker) {
	t.Helper()
	address, accepted := testBroker(t)
	shorterBackoff(t)
	sweep := &sweeper{
		library:     "house/movies",
		job:         "cleanup-1",
		catalog:     catalog,
		echoTimeout: scanTestTimeout,
		log:         &syncLog{},
	}
	sweep.echo = newEchoWaiter(libraryStatusTopic(defaultTopicBase, "house", "movies"), workerCleanup, sweep.job)
	sweep.bus = newBus(address, "cleanup-house-movies", nil, nil, sweep.echo.note)
	return sweep, accepted
}

// a log written from the job's goroutine and read by the test, with the
// lock that makes that safe.
type syncLog struct {
	mutex sync.Mutex
	text  strings.Builder
}

func (l *syncLog) Write(line []byte) (int, error) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.text.Write(line)
}

func (l *syncLog) String() string {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.text.String()
}

// The Job takes every row the departing library holds, the runs of
// every other worker with them, writes its own run last, and exits on the
// echo. The surviving library is untouched.
func TestTheCleanupJobSweepsAndWaitsForItsEcho(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	seedTwoLibrariesInEveryTable(t, catalog)
	if err := catalog.UpsertRun(t.Context(), "house/movies",
		libraryRun{Worker: workerScan, Job: "scan-1", Started: time.Unix(10, 0), Finished: time.Unix(20, 0)}); err != nil {
		t.Fatal(err)
	}
	sweep, accepted := cleanupJob(t, catalog)

	done := make(chan error, 1)
	go func() { done <- sweep.runJob(t.Context()) }()
	echoTheCleanup(t, accepted, sweep.echo)
	if err := <-done; err != nil {
		t.Fatalf("the job failed: %v", err)
	}

	if got := agent.rowsFor(t, "movies", "house/movies"); got != 0 {
		t.Errorf("the departed library holds %d movie rows, want none", got)
	}
	if got := agent.rowsFor(t, "movies", "house/series"); got == 0 {
		t.Error("the surviving library lost its rows")
	}
	runs, err := catalog.Runs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	held := runs["house/movies"]
	if len(held) != 1 || held[0].Worker != workerCleanup || held[0].Job != "cleanup-1" {
		t.Fatalf("the library holds %+v, want the cleanup run alone", held)
	}
	if held[0].Finished.IsZero() {
		t.Error("the cleanup run carries no finish time")
	}
}

// Answers the Job's subscription with the report the reporter
// would publish once it holds the deletes.
func echoTheCleanup(t *testing.T, accepted <-chan *fakeBroker, wait *echoWaiter) {
	t.Helper()
	broker := waitForBroker(t, accepted)
	if got := waitForString(t, broker.subs); got != wait.topic {
		t.Fatalf("the Job subscribed to %q, want %q", got, wait.topic)
	}
	broker.push(wait.topic, reportOf(t, libraryRun{
		Worker: wait.worker, Job: wait.job,
		Started: time.Unix(10, 0), Finished: time.Unix(20, 0),
	}))
}

// The cleanup waits for a report that counts no item and no file
// for the library, because a report that still counts either is one
// whose deletes have not reached the standing pod.
func TestTheCleanupJobWaitsUntilTheLibraryIsEmpty(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedTwoLibrariesInEveryTable(t, catalog)
	sweep, accepted := cleanupJob(t, catalog)
	done := make(chan error, 1)
	go func() { done <- sweep.runJob(t.Context()) }()

	broker := waitForBroker(t, accepted)
	if got := waitForString(t, broker.subs); got != sweep.echo.topic {
		t.Fatalf("the Job subscribed to %q, want %q", got, sweep.echo.topic)
	}
	run := libraryRun{Worker: workerCleanup, Job: "cleanup-1", Started: time.Unix(10, 0), Finished: time.Unix(20, 0)}
	broker.push(sweep.echo.topic, mustMarshal(t, libraryReport{Items: 3, Files: 2, Runs: []libraryRun{run}}))
	select {
	case err := <-done:
		t.Fatalf("the job exited on a report that still counts the rows it deleted: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	broker.push(sweep.echo.topic, mustMarshal(t, libraryReport{Runs: []libraryRun{run}}))

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the job failed: %v", err)
		}
	case <-time.After(scanTestTimeout):
		t.Fatal("the job never exited on the report of an empty library")
	}
}

// An echo that never arrives fails the Job, so the rows stay on
// its own claim and the retry carries them.
func TestTheCleanupJobFailsWithNoEcho(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	sweep, _ := cleanupJob(t, catalog)
	sweep.echoTimeout = 20 * time.Millisecond

	err := sweep.runJob(t.Context())

	if err == nil {
		t.Fatal("the job returned no error, want the echo timeout")
	}
	if !strings.Contains(err.Error(), "cleanup-1") {
		t.Errorf("error = %v, want the Job named", err)
	}
}

// a failed sweep names the failure in the log and fails the Job, because
// a Job that waited on an echo of rows it never deleted would time out
// with nothing to show.
func TestTheSweepLogsAFailureAndFailsTheJob(t *testing.T) {
	unwell := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("the agent is unwell"))
	}))
	t.Cleanup(unwell.Close)
	var logged bytes.Buffer
	sweep := &sweeper{
		library: "house/movies",
		job:     "cleanup-1",
		catalog: NewCatalog(unwell.URL, unwell.Client()),
		log:     &logged,
	}

	if err := sweep.runJob(t.Context()); err == nil {
		t.Error("the job returned no error, want the failed sweep")
	}

	if !strings.Contains(logged.String(), "could not sweep house/movies") {
		t.Errorf("log = %q, want the failure named", logged.String())
	}
	if !strings.Contains(logged.String(), "the agent is unwell") {
		t.Errorf("log = %q, want the agent's own message", logged.String())
	}
}

// A catalog that answers the sweep and refuses the runs delete
// fails the Job, so a library that kept another worker's run is never
// reported as departed.
func TestTheSweepFailsWhenTheRunsStay(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedTwoLibrariesInEveryTable(t, catalog)
	var logged bytes.Buffer
	sweep := &sweeper{
		library: "house/movies",
		job:     "cleanup-1",
		catalog: refusingRunDeletes(t, catalog),
		log:     &logged,
	}

	if err := sweep.runJob(t.Context()); err == nil {
		t.Error("the job returned no error, want the refused delete")
	}
	if !strings.Contains(logged.String(), "could not sweep the runs of house/movies") {
		t.Errorf("log = %q, want the failure named", logged.String())
	}
}

// An agent that answers every write but the delete of the runs,
// which is the one write of the sweep with no batch behind it.
func refusingRunDeletes(t *testing.T, catalog *Catalog) *Catalog {
	t.Helper()
	return proxyCatalog(t, catalog, func(path string, body []byte) bool {
		return bytes.Contains(body, []byte("DELETE FROM runs"))
	})
}

// A catalog that refuses the cleanup's own run fails the Job,
// because a Job whose run never landed waits for an echo that cannot
// come.
func TestTheCleanupJobFailsWhenItCannotWriteItsRun(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedTwoLibrariesInEveryTable(t, catalog)
	sweep := &sweeper{
		library: "house/movies",
		job:     "cleanup-1",
		catalog: proxyCatalog(t, catalog, func(path string, body []byte) bool {
			return bytes.Contains(body, []byte("INSERT INTO runs"))
		}),
		log: &bytes.Buffer{},
	}

	if err := sweep.runJob(t.Context()); err == nil {
		t.Error("the job returned no error, want the refused run")
	}
}
