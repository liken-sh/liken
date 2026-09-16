package main

// What these tests prove: the wait a Job makes after its last
// write. It ends on the first confirmation of its own run, it stands on
// another Job's, it writes its run again while it waits, and it fails on
// its timeout.

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The wait a Job gives a confirmer, read out of the environment,
// with the default for every value it cannot use.
func TestTheHandoffTimeoutComesOffTheEnvironment(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{name: "an empty value is the default", raw: "", want: defaultHandoffTimeout},
		{name: "a duration", raw: "45s", want: 45 * time.Second},
		{name: "an unreadable value is the default", raw: "soon", want: defaultHandoffTimeout},
		{name: "a negative value is the default", raw: "-1m", want: defaultHandoffTimeout},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := handoffTimeout(testCase.raw); got != testCase.want {
				t.Errorf("handoffTimeout(%q) = %s, want %s", testCase.raw, got, testCase.want)
			}
		})
	}
}

// ShorterNudge shortens the repeated write for one test.
func shorterNudge(t *testing.T, period time.Duration) {
	t.Helper()
	was := handoffNudge
	t.Cleanup(func() { handoffNudge = was })
	handoffNudge = period
}

// The version every waiting run in these tests names, which a
// confirmation has to carry to end the wait.
const waitingVersion = 4

// One Job's wait against a real catalog, so the subscription and
// the row that ends it travel a real connection.
func waitingJob(t *testing.T, catalog *Catalog, worker, job string) *handoffWaiter {
	t.Helper()
	return newHandoffWaiter(catalog, "house/movies", libraryRun{
		Worker: worker, Job: job, Started: time.Unix(10, 0).UTC(),
		Finished: time.Unix(20, 0).UTC(), Actor: sqliteAgentActor, Version: waitingVersion,
	})
}

// Confirm writes the row one catalog pod writes when it holds a
// Job's versions, at the version the waiting run carries.
func confirm(t *testing.T, catalog *Catalog, worker, job string, version int64) {
	t.Helper()
	confirmOne(t, catalog, "house/movies", worker, job, version)
}

// ConfirmOne writes the confirmation of one library's run.
func confirmOne(t *testing.T, catalog *Catalog, library, worker, job string, version int64) {
	t.Helper()
	if err := catalog.UpsertConfirmation(t.Context(), library, worker, job,
		"movies-catalog-0", version, time.Unix(30, 0)); err != nil {
		t.Fatal(err)
	}
}

// Writes the confirmation one Job waits for, once that Job has
// written the run it hands off on. A Job writes that row after every other
// write it makes, so the confirmation never lands ahead of the work.
func confirmTheRun(t *testing.T, catalog *Catalog, worker, job string) {
	t.Helper()
	confirmTheRunOf(t, catalog, "house/movies", worker, job)
}

// ConfirmTheRunOf is confirmTheRun for a library of any name.
func confirmTheRunOf(t *testing.T, catalog *Catalog, library, worker, job string) {
	t.Helper()
	deadline := time.After(scanTestTimeout)
	for {
		runs, err := catalog.Runs(t.Context())
		if err == nil {
			if run, held := runOf(runs[library], worker); held && run.Job == job && run.Version > 0 {
				confirmOne(t, catalog, library, worker, job, run.Version)
				return
			}
		}
		select {
		case <-deadline:
			t.Fatal("the job never wrote the run it hands off on")
		case <-time.After(time.Millisecond):
		}
	}
}

// The first confirmation of this Job's own run ends the wait, which
// is what lets the Job exit knowing a standing pod holds its rows.
func TestTheConfirmationEndsTheWait(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	wait := waitingJob(t, catalog, workerScan, "scan-1")
	done := make(chan error, 1)
	go func() { done <- wait.wait(t.Context(), scanTestTimeout) }()

	confirm(t, catalog, workerScan, "scan-1", waitingVersion)

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the wait ended with %v, want the confirmation", err)
		}
	case <-time.After(scanTestTimeout):
		t.Fatal("the confirmation never ended the wait")
	}
}

// A confirmation of a run that is not this pod's leaves the wait
// standing, so a Job never reads another worker's handoff, or the handoff
// of a pod of its own Job that died before it, as its own.
func TestTheWaitStandsOnAConfirmationThatIsNotItsOwn(t *testing.T) {
	cases := []struct {
		name    string
		worker  string
		job     string
		version int64
	}{
		{name: "another worker's run", worker: workerCleanup, job: "scan-1", version: waitingVersion},
		{name: "another Job's run", worker: workerScan, job: "scan-0", version: waitingVersion},
		// A pod of this Job that timed out before this one wrote a
		// version of its own, and its confirmation proves that version alone.
		{name: "an earlier pod of this Job", worker: workerScan, job: "scan-1", version: waitingVersion - 1},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			shorterNudge(t, time.Hour)
			catalog, _ := newSQLiteCatalog(t)
			wait := waitingJob(t, catalog, workerScan, "scan-1")
			done := make(chan error, 1)
			go func() { done <- wait.wait(t.Context(), 150*time.Millisecond) }()

			confirm(t, catalog, testCase.worker, testCase.job, testCase.version)

			if err := <-done; err == nil {
				t.Error("the wait ended on a confirmation that names no run of this Job")
			}
		})
	}
}

// The wait writes its run again every nudge, with a later finish
// each time, so each write is a version with a broadcast of its own.
func TestTheWaitWritesItsRunAgainWhileItStands(t *testing.T) {
	shorterNudge(t, 10*time.Millisecond)
	catalog, agent := newSQLiteCatalog(t)
	wait := waitingJob(t, catalog, workerScan, "scan-1")
	done := make(chan error, 1)
	go func() { done <- wait.wait(t.Context(), scanTestTimeout) }()

	first := finishedAtLeast(t, agent, 2)
	confirm(t, catalog, workerScan, "scan-1", waitingVersion)
	if err := <-done; err != nil {
		t.Fatalf("the wait ended with %v, want the confirmation", err)
	}

	if first <= time.Unix(20, 0).Unix() {
		t.Errorf("the run was written again at %d, want a time past its finish", first)
	}
}

// FinishedAtLeast waits until the runs row has been written the
// given number of times, and answers with the finish it then carries.
func finishedAtLeast(t *testing.T, agent *sqliteAgent, writes int64) int64 {
	t.Helper()
	deadline := time.After(scanTestTimeout)
	for {
		finished := int64(0)
		version := int64(0)
		row := agent.db.QueryRow(`SELECT finished, (SELECT db_version FROM crsql_db_versions LIMIT 1) FROM runs`)
		if err := row.Scan(&finished, &version); err == nil && version >= writes {
			return finished
		}
		select {
		case <-deadline:
			t.Fatal("the wait never wrote its run again")
		case <-time.After(time.Millisecond):
		}
	}
}

// A write that fails is one log line and not the end of the wait,
// because a confirmer may already hold what the first write carried.
func TestAFailedWriteDoesNotEndTheWait(t *testing.T) {
	shorterNudge(t, 10*time.Millisecond)
	catalog, _ := newSQLiteCatalog(t)
	log := &syncLog{}
	refusing := proxyCatalog(t, catalog, func(path string, body []byte) bool {
		return strings.HasSuffix(path, transactionsPath) && strings.Contains(string(body), "INSERT INTO runs")
	})
	wait := waitingJob(t, refusing, workerScan, "scan-1")
	wait.log = log
	done := make(chan error, 1)
	go func() { done <- wait.wait(t.Context(), scanTestTimeout) }()

	waitForLog(t, log, "scan-1")
	confirm(t, catalog, workerScan, "scan-1", waitingVersion)

	if err := <-done; err != nil {
		t.Fatalf("the wait ended with %v, want the confirmation", err)
	}
}

// WaitForLog stands until the log holds this text.
func waitForLog(t *testing.T, log *syncLog, text string) {
	t.Helper()
	deadline := time.After(scanTestTimeout)
	for {
		if strings.Contains(log.String(), text) {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("the log reads %q, want %q in it", log.String(), text)
		case <-time.After(time.Millisecond):
		}
	}
}

// A confirmation that never arrives fails the Job, so its rows stay
// on its own claim and the retry carries them. The failure names the
// worker, the Job, and how long it waited.
func TestTheWaitFailsOnItsTimeout(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	wait := waitingJob(t, catalog, workerScan, "scan-1")

	err := wait.wait(t.Context(), 10*time.Millisecond)

	if err == nil {
		t.Fatal("the wait ended with no error, want the timeout")
	}
	for _, want := range []string{workerScan, "scan-1", "10ms"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want %q named", err, want)
		}
	}
}

// A cancelled context ends the wait, so a Job the kubelet stops
// does not hold the pod open for the whole timeout.
func TestTheWaitEndsOnACancelledContext(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	wait := waitingJob(t, catalog, workerScan, "scan-1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := wait.wait(ctx, scanTestTimeout); err == nil {
		t.Error("the wait ended with no error, want the cancelled context")
	}
}

// A subscription that ends is opened again, so a Job whose agent
// dropped the stream still hears the confirmation that follows.
func TestTheWaitOpensTheStreamAgain(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	var refused atomic.Bool
	flaky := proxyCatalog(t, catalog, func(path string, body []byte) bool {
		return strings.HasSuffix(path, subscriptionsPath) && refused.CompareAndSwap(false, true)
	})
	wait := waitingJob(t, flaky, workerScan, "scan-1")
	done := make(chan error, 1)
	go func() { done <- wait.wait(t.Context(), scanTestTimeout) }()

	confirm(t, catalog, workerScan, "scan-1", waitingVersion)

	if err := <-done; err != nil {
		t.Fatalf("the wait ended with %v, want the confirmation after the stream reopened", err)
	}
}

// Every repeat moves the finish time by at least one second,
// because the runs table holds seconds.
func TestTheNextFinishAlwaysMoves(t *testing.T) {
	was := time.Unix(1_700_000_000, 0).UTC()
	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{name: "a later second", now: was.Add(10 * time.Second), want: was.Add(10 * time.Second)},
		{name: "the same second", now: was.Add(20 * time.Millisecond), want: was.Add(time.Second)},
		{name: "an earlier second", now: was.Add(-time.Minute), want: was.Add(time.Second)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := nextFinish(was, testCase.now); !got.Equal(testCase.want) {
				t.Errorf("nextFinish = %v, want %v", got, testCase.want)
			}
		})
	}
}

// The hand-off writes the finished run, then writes it again with
// the actor and version that write answered with, so a confirmer reads the
// write it has to hold off the row itself.
func TestTheHandOffWritesTheRunWithTheWriteThatMadeIt(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	run := libraryRun{Worker: workerScan, Job: "scan-1",
		Started: time.Unix(10, 0).UTC(), Finished: time.Unix(20, 0).UTC()}
	done := make(chan error, 1)
	go func() { done <- handOff(t.Context(), catalog, "house/movies", run, nil, scanTestTimeout) }()

	actor, version := runWriteOf(t, agent)
	confirm(t, catalog, workerScan, "scan-1", version)
	if err := <-done; err != nil {
		t.Fatalf("the hand-off failed: %v", err)
	}

	if actor != sqliteAgentActor {
		t.Errorf("the run names actor %q, want the agent that wrote it", actor)
	}
	if version != 1 {
		t.Errorf("the run names version %d, want the version of the finished write", version)
	}
}

// RunWriteOf reads the actor and version the runs row carries, once
// the Job has written them.
func runWriteOf(t *testing.T, agent *sqliteAgent) (string, int64) {
	t.Helper()
	deadline := time.After(scanTestTimeout)
	for {
		actor, version := "", int64(0)
		row := agent.db.QueryRow(`SELECT actor, version FROM runs`)
		if err := row.Scan(&actor, &version); err == nil && version > 0 {
			return actor, version
		}
		select {
		case <-deadline:
			t.Fatal("the run never named the write that made it")
		case <-time.After(time.Millisecond):
		}
	}
}

// A hand-off whose first write fails never waits, because there is
// no run for a confirmer to hold.
func TestTheHandOffFailsWhereTheRunCannotBeWritten(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	agent.transactionsLeft = 1
	run := libraryRun{Worker: workerScan, Job: "scan-1", Finished: time.Unix(20, 0).UTC()}

	if err := handOff(t.Context(), catalog, "house/movies", run, nil, 10*time.Millisecond); err == nil {
		t.Error("the hand-off waited on a run it could not write")
	}
}

// The second write is the one that names the write a confirmer has
// to hold, so a hand-off that cannot make it fails rather than waiting.
func TestTheHandOffFailsWhereTheRunCannotNameItsWrite(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	agent.transactionsLeft = 2
	run := libraryRun{Worker: workerScan, Job: "scan-1", Finished: time.Unix(20, 0).UTC()}

	if err := handOff(t.Context(), catalog, "house/movies", run, nil, 10*time.Millisecond); err == nil {
		t.Error("the hand-off waited on a run that names no write")
	}
}
