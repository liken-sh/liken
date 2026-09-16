package main

// What these tests run: the confirmer against a catalog loaded with
// the shipped schema and the cr-sqlite bookkeeping a real agent holds, so
// the run stream, the version read, and the row it writes all run with no
// pod.

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// The name every confirmer in these tests writes its row under.
const testConfirmerPod = "movies-catalog-0"

// One confirmer over this catalog, with its recheck in
// milliseconds, so a test proves the retry in the time a tick takes.
func testConfirmer(t *testing.T, catalog *Catalog, log io.Writer) *confirmer {
	t.Helper()
	was := confirmerRecheck
	t.Cleanup(func() { confirmerRecheck = was })
	confirmerRecheck = 5 * time.Millisecond
	backoffWas := reportMinBackoff
	t.Cleanup(func() { reportMinBackoff = backoffWas })
	reportMinBackoff = 5 * time.Millisecond

	return &confirmer{
		name:      testConfirmerPod,
		catalog:   catalog,
		log:       log,
		confirmed: map[string]bool{},
		pending:   map[string]finishedRun{},
	}
}

// Runs one confirmer for the life of the test, the way the
// container runs it for the life of the pod.
func serving(t *testing.T, work *confirmer) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		work.serve(ctx)
	}()
	t.Cleanup(func() {
		stop()
		<-done
	})
}

// Writes the two rows a finished Job leaves: the run, and the run
// again naming the write that made it.
func finishedRunOf(t *testing.T, catalog *Catalog, library, worker, job string) libraryRun {
	t.Helper()
	run := libraryRun{Worker: worker, Job: job,
		Started: time.Unix(10, 0).UTC(), Finished: time.Unix(20, 0).UTC()}
	actor, version, err := catalog.UpsertRun(t.Context(), library, run)
	if err != nil {
		t.Fatal(err)
	}
	run.Actor, run.Version = actor, version
	if _, _, err := catalog.UpsertRun(t.Context(), library, run); err != nil {
		t.Fatal(err)
	}
	return run
}

// Stands until the catalog holds this pod's confirmation of one run
// at one version, and fails when it never does.
func awaitConfirmation(t *testing.T, catalog *Catalog, library, worker, job string, version int64) {
	t.Helper()
	deadline := time.After(scanTestTimeout)
	for {
		held, err := catalog.confirmedBy(t.Context(), library, worker, job, testConfirmerPod, version)
		if err == nil && held {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("no confirmation of the %s run %s of %s", worker, job, library)
		case <-time.After(time.Millisecond):
		}
	}
}

// A finished run whose versions this copy holds is confirmed, and
// the confirmer names it in one log line.
func TestTheConfirmerConfirmsARunItsCopyHolds(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	log := &syncLog{}
	serving(t, testConfirmer(t, catalog, log))

	run := finishedRunOf(t, catalog, "house/movies", workerScan, "scan-1")

	awaitConfirmation(t, catalog, "house/movies", workerScan, "scan-1", run.Version)
	waitForLog(t, log, "confirmed the scan run scan-1 of house/movies")
}

// A run whose versions this copy is missing stands unconfirmed
// until the gap fills, which is what keeps a Job from exiting before a
// standing copy holds its rows.
func TestTheConfirmerWaitsUntilItsCopyHoldsTheVersions(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	other := "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	if _, _, err := catalog.UpsertRun(t.Context(), "house/movies", libraryRun{
		Worker: workerScan, Job: "scan-1", Finished: time.Unix(20, 0),
		Actor: other, Version: 12,
	}); err != nil {
		t.Fatal(err)
	}
	work := testConfirmer(t, catalog, io.Discard)
	serving(t, work)

	held, err := catalog.confirmedBy(t.Context(), "house/movies", workerScan, "scan-1", testConfirmerPod, 12)
	if err != nil {
		t.Fatal(err)
	}
	if held {
		t.Fatal("the confirmer confirmed a run whose versions its copy does not hold")
	}

	agent.holdVersion(t, other, 12)

	awaitConfirmation(t, catalog, "house/movies", workerScan, "scan-1", 12)
}

// A run that names no write is a run whose Job has not answered for
// itself yet, so no confirmer acts on it.
func TestTheConfirmerSkipsARunThatNamesNoWrite(t *testing.T) {
	cases := []struct {
		name    string
		columns []string
		cells   []any
	}{
		{
			name:    "a run with no version",
			columns: []string{"library", "worker", "job", "actor", "version"},
			cells:   []any{"house/movies", "scan", "scan-1", sqliteAgentActor, float64(0)},
		},
		{
			name:    "a row with no version column",
			columns: []string{"library", "worker", "job", "actor"},
			cells:   []any{"house/movies", "scan", "scan-1", sqliteAgentActor},
		},
		{
			name:    "a row with no library",
			columns: []string{"library", "worker", "job", "actor", "version"},
			cells:   []any{"", "scan", "scan-1", sqliteAgentActor, float64(4)},
		},
		{
			name:    "a row with no Job",
			columns: []string{"library", "worker", "job", "actor", "version"},
			cells:   []any{"house/movies", "scan", "", sqliteAgentActor, float64(4)},
		},
		{
			name:    "a library that is not a string",
			columns: []string{"library", "worker", "job", "actor", "version"},
			cells:   []any{float64(7), "scan", "scan-1", sqliteAgentActor, float64(4)},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, ok := decodeFinishedRun(testCase.columns, testCase.cells); ok {
				t.Error("the confirmer read a run out of a row that names no write")
			}
		})
	}
}

// The matcher prepends the primary key to the projection, so the
// confirmer reads every column by name.
func TestTheConfirmerReadsItsColumnsByName(t *testing.T) {
	run, ok := decodeFinishedRun(
		[]string{"worker", "library", "job", "actor", "version"},
		[]any{"scan", "house/movies", "scan-1", sqliteAgentActor, float64(4)})

	if !ok {
		t.Fatal("the confirmer read no run out of a row with its key columns first")
	}
	want := finishedRun{library: "house/movies", worker: "scan", job: "scan-1",
		actor: sqliteAgentActor, version: 4}
	if run != want {
		t.Errorf("run = %+v, want %+v", run, want)
	}
}

// The confirmation of the Job before this one leaves with this
// one's arrival, so the table holds the newest run of each worker.
func TestTheConfirmerTakesTheOlderJobOfTheSameWorker(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	if err := catalog.UpsertConfirmation(t.Context(), "house/movies", workerScan, "scan-0",
		testConfirmerPod, 1, time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	serving(t, testConfirmer(t, catalog, io.Discard))

	run := finishedRunOf(t, catalog, "house/movies", workerScan, "scan-1")

	awaitConfirmation(t, catalog, "house/movies", workerScan, "scan-1", run.Version)
	awaitRowCount(t, agent, "confirmations", 1)
}

// A retried pod of one Job carries the Job's name and writes a version
// of its own, so the confirmer confirms that run again and moves its row
// to the newer version. Without this, the retry would read the dead pod's
// confirmation as its own and exit with its rows unproved.
func TestTheConfirmerConfirmsARunAgainWhenItsVersionMoves(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	serving(t, testConfirmer(t, catalog, io.Discard))
	first := finishedRunOf(t, catalog, "house/movies", workerScan, "scan-1")
	awaitConfirmation(t, catalog, "house/movies", workerScan, "scan-1", first.Version)

	second := finishedRunOf(t, catalog, "house/movies", workerScan, "scan-1")

	if second.Version <= first.Version {
		t.Fatalf("the retry wrote version %d, want one past the pod before it", second.Version)
	}
	awaitConfirmation(t, catalog, "house/movies", workerScan, "scan-1", second.Version)
	awaitRowCount(t, agent, "confirmations", 1)
}

// Stands until one table holds this many rows. The confirmer writes
// its row and takes the older Job's in two writes, so the count a test
// reads settles one write after the confirmation arrives.
func awaitRowCount(t *testing.T, agent *sqliteAgent, table string, want int) {
	t.Helper()
	deadline := time.After(scanTestTimeout)
	for {
		got := agent.rowCount(t, table)
		if got == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("%s holds %d rows, want %d", table, got, want)
		case <-time.After(time.Millisecond):
		}
	}
}

// A run this pod already confirmed is left where it is, so a
// confirmer that started again writes no version for a row that stands.
func TestTheConfirmerLeavesAConfirmationItAlreadyWrote(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	run := finishedRunOf(t, catalog, "house/movies", workerScan, "scan-1")
	if err := catalog.UpsertConfirmation(t.Context(), "house/movies", workerScan, "scan-1",
		testConfirmerPod, run.Version, time.Unix(30, 0)); err != nil {
		t.Fatal(err)
	}
	before := agent.version
	work := testConfirmer(t, catalog, io.Discard)

	work.settle(t.Context(), finishedRun{library: "house/movies", worker: workerScan,
		job: "scan-1", actor: run.Actor, version: run.Version})

	if agent.version != before {
		t.Errorf("the catalog took %d writes, want none for a confirmation that stands",
			agent.version-before)
	}
}

// A run the confirmer has already settled in this process is read
// no further, so one stream of changes costs one read per run.
func TestTheConfirmerSettlesEachRunOnce(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	run := finishedRunOf(t, catalog, "house/movies", workerScan, "scan-1")
	work := testConfirmer(t, catalog, io.Discard)
	held := finishedRun{library: "house/movies", worker: workerScan, job: "scan-1",
		actor: run.Actor, version: run.Version}

	work.settle(t.Context(), held)
	before := len(work.pending)
	work.settle(t.Context(), held)

	if before != 0 || len(work.pending) != 0 {
		t.Errorf("the confirmer holds %d runs, want none after it confirmed them", len(work.pending))
	}
	if !work.done(held.key()) {
		t.Error("the confirmer did not mark the run it confirmed")
	}
}

// An agent that will not answer is one log line and a run held for
// the next recheck, never a run read as confirmed.
func TestAnAgentThatWillNotAnswerHoldsTheRun(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	run := finishedRunOf(t, catalog, "house/movies", workerScan, "scan-1")
	log := &bytes.Buffer{}
	work := testConfirmer(t, catalog, log)
	agent.queriesLeft = 1

	work.settle(t.Context(), finishedRun{library: "house/movies", worker: workerScan,
		job: "scan-1", actor: run.Actor, version: run.Version})

	if len(work.pending) != 1 {
		t.Errorf("the confirmer holds %d runs, want the one it could not read", len(work.pending))
	}
	if !strings.Contains(log.String(), "could not confirm the scan run scan-1") {
		t.Errorf("log = %q, want the failed read named", log.String())
	}
}

// A confirmer built without a log writes nowhere, so the role runs
// in a test with no output of its own.
func TestAConfirmerWithNoLogWritesNothing(t *testing.T) {
	work := &confirmer{}

	work.logf("confirmed nothing")
}

// The confirmer reads its pod name and the agent's address out of
// the environment, the only place a pod with no API credential learns them.
func TestTheConfirmerReadsItsPodOutOfTheEnvironment(t *testing.T) {
	t.Setenv(podNameVariable, testConfirmerPod)
	t.Setenv(catalogAPIVariable, "")
	log := &bytes.Buffer{}

	work := newConfirmer(log)

	if work.name != testConfirmerPod {
		t.Errorf("name = %q, want the pod the environment names", work.name)
	}
	if work.catalog.base != defaultCatalogAPI {
		t.Errorf("catalog = %q, want the loopback agent %s", work.catalog.base, defaultCatalogAPI)
	}
	if !strings.Contains(log.String(), testConfirmerPod) {
		t.Errorf("log = %q, want the copy it confirms against", log.String())
	}
}

// A run stream that ends is opened again, so a confirmer whose
// agent dropped the stream keeps confirming.
func TestTheConfirmerOpensTheRunStreamAgain(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	log := &syncLog{}
	refused := 0
	flaky := proxyCatalog(t, catalog, func(path string, body []byte) bool {
		if !strings.HasSuffix(path, subscriptionsPath) {
			return false
		}
		refused++
		return refused == 1
	})
	work := testConfirmer(t, flaky, log)
	serving(t, work)
	waitForLog(t, log, "the run stream ended")

	run := finishedRunOf(t, catalog, "house/movies", workerScan, "scan-1")

	awaitConfirmation(t, catalog, "house/movies", workerScan, "scan-1", run.Version)
}
