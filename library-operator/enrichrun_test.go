package main

import (
	"bytes"
	"net/http"
	"testing"
	"time"
)

// EnrichJob builds the enricher Job's closing container over one
// catalog, so the runs row and the hand-off both travel a real connection.
func enrichJob(t *testing.T, catalog *Catalog) *enrichRun {
	t.Helper()
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), catalog)
	work.job = "movies-enrich-1"

	return &enrichRun{enricher: work, handoffTimeout: scanTestTimeout}
}

func TestTheEnrichJobWritesItsRunAndWaitsToBeConfirmed(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	run := enrichJob(t, catalog)
	done := make(chan error, 1)
	go func() { done <- run.runJob(t.Context()) }()

	confirmTheRun(t, catalog, workerEnrich, run.job)
	if err := <-done; err != nil {
		t.Fatalf("the job failed: %v", err)
	}

	if got := agent.rowCount(t, "runs"); got != 1 {
		t.Fatalf("runs = %d, want the one row this worker holds", got)
	}
	worker, job, finished := oneRun(t, agent)
	if worker != workerEnrich || job != "movies-enrich-1" {
		t.Errorf("the run names %s/%s, want the enrich worker and this Job", worker, job)
	}
	if finished == 0 {
		t.Error("the run carries no finish time")
	}
}

func TestTheEnrichJobKeepsTheStartTheProbeContainerWrote(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	run := enrichJob(t, catalog)
	if err := run.markRunStarted(t.Context()); err != nil {
		t.Fatal(err)
	}
	started := run.startedAt(t.Context())

	done := make(chan error, 1)
	go func() { done <- run.runJob(t.Context()) }()
	confirmTheRun(t, catalog, workerEnrich, run.job)
	if err := <-done; err != nil {
		t.Fatalf("the job failed: %v", err)
	}

	if started.IsZero() {
		t.Error("the container read no start off the row the probe wrote")
	}
}

func TestTheStartOfAnotherJobIsNotThisOnes(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	run := enrichJob(t, catalog)
	_, _, err := catalog.UpsertRun(t.Context(), run.library, libraryRun{
		Worker: workerEnrich, Job: "another-job", Started: time.Unix(10, 0),
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := run.startedAt(t.Context()); got.Equal(time.Unix(10, 0)) {
		t.Error("the container took another Job's start as its own")
	}
}

func TestTheEnrichJobFailsWhereItCannotReachItsAgent(t *testing.T) {
	run := enrichJob(t, NewCatalog("http://127.0.0.1:1", &http.Client{Timeout: time.Second}))

	if err := run.runJob(t.Context()); err == nil {
		t.Error("the job reported no error, want the unreachable agent's")
	}
}

// The container reads the Job it closes and the wait it makes out of
// the environment, because it holds no credential to look a Library up with.
func TestAnEnrichJobReadsItsJobOutOfTheEnvironment(t *testing.T) {
	t.Setenv(libraryNamespaceVariable, "house")
	t.Setenv(libraryNameVariable, "movies")
	t.Setenv(jobNameVariable, "movies-enrich-1")
	t.Setenv(handoffTimeoutVariable, "")
	t.Setenv(libraryWorkerVariable, workerEnrich)

	run, err := newEnrichRun(&bytes.Buffer{})

	if err != nil {
		t.Fatal(err)
	}
	if run.job != "movies-enrich-1" {
		t.Errorf("job = %q, want the Job the environment names", run.job)
	}
	if run.handoffTimeout != defaultHandoffTimeout {
		t.Errorf("timeout = %s, want the default", run.handoffTimeout)
	}
}

// oneRun reads the one runs row the catalog holds, so a test names the worker
// and the Job without a query of its own.
func oneRun(t *testing.T, agent *sqliteAgent) (string, string, int64) {
	t.Helper()
	var worker, job string
	var finished int64
	if err := agent.db.QueryRow(`SELECT worker, job, finished FROM runs`).Scan(&worker, &job, &finished); err != nil {
		t.Fatal(err)
	}
	return worker, job, finished
}
