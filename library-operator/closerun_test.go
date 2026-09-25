package main

// The tests of the close container: the start it writes first, the wait for
// every phase's mark, the failures it copies into the runs row, the sets it
// derives, and the hand-off it ends on.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The close container of one Job over one catalog, waiting for the phases
// named, on a phases volume of its own.
func closingJob(t *testing.T, catalog *Catalog, needs ...string) (*closeRun, *bytes.Buffer) {
	t.Helper()
	work, log := testEnricher(t, libraryKindMovies, t.TempDir(), catalog)
	work.job = "movies-walk-1"
	work.needs = needs
	work.board = newPhaseBoard(t.TempDir())
	shortSettle(t)
	return &closeRun{enricher: work, handoffTimeout: scanTestTimeout}, log
}

// The settle time is milliseconds here, so a wake reads the marks at once.
func shortSettle(t *testing.T) {
	t.Helper()
	was := phaseSettle
	t.Cleanup(func() { phaseSettle = was })
	phaseSettle = time.Millisecond
}

// The container writes the start while the phases run, waits for their
// marks, and writes the finished run with the failure one phase wrote.
func TestTheCloseContainerWaitsForEveryPhaseAndNamesTheFailures(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	run, _ := closingJob(t, catalog, factProbe, factIdentity)
	probe, identity := newPhaseBoard(run.board.dir), newPhaseBoard(run.board.dir)
	for _, phase := range []struct {
		board *phaseBoard
		name  string
	}{{probe, factProbe}, {identity, factIdentity}} {
		if err := phase.board.start(phase.name); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() { done <- run.runJob(t.Context()) }()

	started := awaitRun(t, catalog, func(held libraryRun) bool { return !held.Started.IsZero() })
	if !started.Finished.IsZero() {
		t.Fatalf("run = %+v, want a start with no finish while the phases run", started)
	}
	if err := probe.finish(factProbe, nil); err != nil {
		t.Fatal(err)
	}
	if err := identity.finish(factIdentity, errors.New("the key was refused")); err != nil {
		t.Fatal(err)
	}
	confirmTheRun(t, catalog, workerEnrich, run.job)
	if err := <-done; err != nil {
		t.Fatalf("the job failed: %v", err)
	}

	finished := awaitRun(t, catalog, func(held libraryRun) bool { return !held.Finished.IsZero() })
	if finished.Failure != "the key was refused" {
		t.Errorf("failure = %q, want the identity phase's", finished.Failure)
	}
	if got := agent.rowCount(t, "runs"); got != 1 {
		t.Errorf("runs = %d, want the one row of the enrich worker", got)
	}
}

// The enrich run of this Job, once it matches.
func awaitRun(t *testing.T, catalog *Catalog, matches func(libraryRun) bool) libraryRun {
	t.Helper()
	deadline := time.After(scanTestTimeout)
	for {
		runs, err := catalog.Runs(t.Context())
		if err == nil {
			if held, ok := runOf(runs["house/movies"], workerEnrich); ok && matches(held) {
				return held
			}
		}
		select {
		case <-deadline:
			t.Fatal("the run never reached the state the test waits for")
		case <-time.After(time.Millisecond):
		}
	}
}

// A scan container from an image built before the phases volume writes no
// mark, and its finished runs row ends the wait in the mark's place.
func TestTheCloseContainerTakesAFinishedWalkForTheScansMark(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	run, _ := closingJob(t, catalog, scanPhase)
	done := make(chan error, 1)
	go func() { done <- run.runJob(t.Context()) }()

	awaitRun(t, catalog, func(held libraryRun) bool { return !held.Started.IsZero() })
	if _, _, err := catalog.UpsertRun(t.Context(), run.library, libraryRun{
		Worker: workerScan, Job: run.job, Started: time.Now().UTC(), Finished: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	confirmTheRun(t, catalog, workerEnrich, run.job)

	if err := <-done; err != nil {
		t.Fatalf("the job failed: %v", err)
	}
}

// The close container derives every set of a movies library again, from
// the member rows the phases wrote, and deletes a set with no member left.
func TestTheCloseContainerDerivesTheSets(t *testing.T) {
	root := setTree(t,
		setTitle{folder: "Quiet Harbor (1998)", title: "Quiet Harbor", released: "1998-06-12", tmdb: "1", set: "Quiet Harbor Collection", colID: "1570"},
		setTitle{folder: "Northwind (1970)", title: "Northwind", released: "1970-03-04", tmdb: "3", set: "Northwind Trilogy"},
	)
	scan, agent := sqliteScanner(t, root)
	scan.fullWalk(context.Background())
	// The art phase writes a title's art column, and the Northwind title
	// leaves its set.
	if _, err := agent.db.Exec(`UPDATE movies SET art = 'Quiet Harbor (1998)/poster.jpg' WHERE title = 'Quiet Harbor'`); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.db.Exec(`UPDATE movies SET set_id = '' WHERE title = 'Northwind'`); err != nil {
		t.Fatal(err)
	}
	run, _ := closingJob(t, scan.catalog)
	done := make(chan error, 1)
	go func() { done <- run.runJob(t.Context()) }()
	confirmTheRun(t, scan.catalog, workerEnrich, run.job)
	if err := <-done; err != nil {
		t.Fatalf("the job failed: %v", err)
	}

	if got := setRowOf(t, agent, "house/movies", "set:tmdb:1570").Art; got != "Quiet Harbor (1998)/poster.jpg" {
		t.Errorf("art = %q, want the art the phase wrote", got)
	}
	if count := agent.rowCount(t, "sets"); count != 1 {
		t.Errorf("sets = %d, want the set with no member gone", count)
	}
}

// seedOldTallies writes one row this worker left more than the retention ago,
// so a test can prove the Job that follows deletes it.
func seedOldTallies(t *testing.T, catalog *Catalog, library, worker string) {
	t.Helper()
	old := newTallies(catalog, library, worker, "a-run-that-ended", factProbe,
		time.Now().UTC().Add(-tallyRetention-time.Hour))
	old.add(tallyAttempts, 1, "fact", factProbe, "result", attemptFound)
	if err := old.flush(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// The Job deletes its worker's old counts where it writes its start, so the
// table holds the retention and no more.
func TestTheCloseContainerSweepsTheOldTallies(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	run, _ := closingJob(t, catalog)
	seedOldTallies(t, catalog, run.library, workerEnrich)
	done := make(chan error, 1)
	go func() { done <- run.runJob(t.Context()) }()
	confirmTheRun(t, catalog, workerEnrich, run.job)
	if err := <-done; err != nil {
		t.Fatalf("the job failed: %v", err)
	}

	if held := talliesHeld(t, agent, run.library); len(held) != 0 {
		t.Errorf("the table holds %v, want the old run's rows gone", held)
	}
}

// A sweep the catalog refuses is logged and never ends the run, because a
// count is not the work.
func TestASweepTheCatalogRefusesNeverEndsTheRun(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	run, log := closingJob(t, catalog)
	seedOldTallies(t, catalog, run.library, workerEnrich)
	agent.transactionsLeft = 2
	ctx, stop := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer stop()

	_ = run.runJob(ctx)

	if !strings.Contains(log.String(), "could not sweep the tallies") {
		t.Errorf("log = %q, want the refused sweep", log.String())
	}
}

// A container that cannot reach its agent writes no start and fails.
func TestTheCloseContainerFailsWhereItCannotReachItsAgent(t *testing.T) {
	run, _ := closingJob(t, NewCatalog("http://127.0.0.1:1", &http.Client{Timeout: time.Second}))

	if err := run.runJob(t.Context()); err == nil {
		t.Error("the job reported no error, want the unreachable agent's")
	}
}

// A container stopped while it waits for the phases ends with the stop.
func TestTheCloseContainerEndsItsWaitWhenStopped(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	run, _ := closingJob(t, catalog, factProbe)
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run.runJob(ctx) }()
	awaitRun(t, catalog, func(held libraryRun) bool { return !held.Started.IsZero() })

	stop()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("runJob = %v, want the stop", err)
	}
}

// The container reads the Job it closes and the wait it makes out of the
// environment, because it holds no credential to look a Library up with.
func TestTheCloseContainerReadsItsJobOutOfTheEnvironment(t *testing.T) {
	t.Setenv(libraryNamespaceVariable, "house")
	t.Setenv(libraryNameVariable, "movies")
	t.Setenv(jobNameVariable, "movies-walk-1")
	t.Setenv(handoffTimeoutVariable, "")
	t.Setenv(libraryWorkerVariable, workerEnrich)
	t.Setenv(libraryContainerVariable, closeMode)
	t.Setenv(libraryPhasesVariable, "/phases")
	t.Setenv(libraryPhaseNeedsVariable, "scan,probe")

	run, err := newCloseRun(&bytes.Buffer{})

	if err != nil {
		t.Fatal(err)
	}
	if run.job != "movies-walk-1" || run.handoffTimeout != defaultHandoffTimeout {
		t.Errorf("run = %s, %s, want the Job and the default wait", run.job, run.handoffTimeout)
	}
	if run.board == nil || run.board.dir != "/phases" || strings.Join(run.needs, ",") != "scan,probe" {
		t.Errorf("board = %+v, needs = %v, want the phases volume and both phases", run.board, run.needs)
	}
}

// A container with no worker in its environment is a manifest to repair.
func TestACloseContainerWithNoWorkerFails(t *testing.T) {
	t.Setenv(libraryWorkerVariable, "")

	if _, err := newCloseRun(&bytes.Buffer{}); err == nil {
		t.Error("newCloseRun = nil error with no worker")
	}
}
