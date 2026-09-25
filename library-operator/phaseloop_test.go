package main

// The tests of the phase loop: a phase works on titles as the phases before
// it write their rows, ends once they have ended and a pass after that found
// no work, writes a failed mark in place of an exit, and stops starting
// titles at its time limit.

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// One phase container over one catalog, with a phases volume of its own,
// the waits short enough for a test.
func phaseOf(t *testing.T, catalog *Catalog, container string, needs ...string) *enricher {
	t.Helper()
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), catalog)
	work.container = container
	work.needs = needs
	work.board = newPhaseBoard(t.TempDir())
	work.syncTimeout = scanTestTimeout
	shortSettle(t)
	backoffWas := reportMinBackoff
	t.Cleanup(func() { reportMinBackoff = backoffWas })
	reportMinBackoff = 5 * time.Millisecond
	return work
}

// The tables whose changes open each gap: the ones its query reads, less the
// attempts.
func TestTheGapTablesAreTheTablesAGapQueryReads(t *testing.T) {
	cases := []struct {
		facts []string
		want  []string
	}{
		{facts: []string{factProbe}, want: []string{"files"}},
		{facts: []string{factIdentity}, want: []string{"movies", "series"}},
		{facts: []string{factMarks}, want: []string{"episodes", "file_items", "files", "movies", "series"}},
		{facts: []string{"no such fact"}, want: []string{}},
	}
	for _, one := range cases {
		if got := gapTables(one.facts); !slices.Equal(got, one.want) {
			t.Errorf("gapTables(%v) = %v, want %v", one.facts, got, one.want)
		}
	}
}

// A phase with nothing to wait for fills its gap, writes its mark, and ends.
func TestAPhaseWithNothingToWaitForFillsItsGapAndEnds(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work := phaseOf(t, catalog, factProbe)
	seedProbeGap(t, catalog, work.root, "The Thing (1982)", "The Thing (1982).mkv")
	answering(t, answeringProbe(ffprobeOfOneFile))

	if err := work.runFacts(t.Context(), []string{factProbe}); err != nil {
		t.Fatal(err)
	}

	if end := work.board.ended(factProbe); end != (phaseEnd{ended: true}) {
		t.Errorf("mark = %+v, want a phase that finished", end)
	}
	if paths := probeGap(t, work); len(paths) != 0 {
		t.Errorf("gap = %v, want every file probed", paths)
	}
}

// The probe gap of one phase, read the way the phase reads it.
func probeGap(t *testing.T, work *enricher) []string {
	t.Helper()
	paths, err := work.gaps(t.Context(), factProbe, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// A phase that waits for the walk works on each file the walk writes while
// the walk runs, and ends only after the walk's mark.
func TestAPhaseWorksOnTheWalksRowsAsTheyArrive(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work := phaseOf(t, catalog, factProbe, scanPhase)
	scan := newPhaseBoard(work.board.dir)
	if err := scan.start(scanPhase); err != nil {
		t.Fatal(err)
	}
	answering(t, answeringProbe(ffprobeOfOneFile))
	done := make(chan error, 1)
	go func() { done <- work.runFacts(t.Context(), []string{factProbe}) }()

	seedProbeGap(t, catalog, work.root, "The Thing (1982)", "The Thing (1982).mkv")
	deadline := time.After(scanTestTimeout)
	for len(probeGap(t, work)) != 0 {
		select {
		case <-deadline:
			t.Fatal("the phase never probed the file the walk wrote")
		case <-time.After(5 * time.Millisecond):
		}
	}
	select {
	case err := <-done:
		t.Fatalf("the phase ended while the walk ran: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := scan.finish(scanPhase, nil); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !work.board.ended(factProbe).ended {
		t.Error("the phase ended with no mark")
	}
}

// A walk from a scanner image built before the phases volume writes no mark,
// and its finished runs row ends the wait in the mark's place.
func TestAFinishedWalkRowStandsForTheScansMark(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work := phaseOf(t, catalog, factProbe, scanPhase)
	if work.needsEnded(t.Context()) {
		t.Fatal("the walk read as ended before it wrote a row")
	}
	if _, _, err := catalog.UpsertRun(t.Context(), work.library, libraryRun{
		Worker: workerRescan, Job: work.job, Started: testNow, Finished: testNow,
	}); err != nil {
		t.Fatal(err)
	}

	if !work.needsEnded(t.Context()) {
		t.Error("the walk's finished row did not end the wait")
	}
}

// A phase whose fact fails writes a failed mark and exits zero, so the Job's
// other phases and its close container still run.
func TestAPhaseThatFailsWritesAFailedMark(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work := phaseOf(t, catalog, nfoContainerName)
	seedNFOGap(t, catalog, work.root, "Winter Harbour (2011)", "movie:tmdb:4242")
	work.providers = lineOf()

	if err := work.runFacts(t.Context(), []string{factOverview}); err != nil {
		t.Fatalf("runFacts = %v, want the failure in the mark and no error", err)
	}

	if end := work.board.ended(nfoContainerName); !end.ended || end.failure == "" {
		t.Errorf("mark = %+v, want a failed mark", end)
	}
}

// A phase with a time limit ends at the limit while the phase it waits for
// still runs, and leaves the rest as gaps.
func TestAPhaseEndsAtItsTimeLimit(t *testing.T) {
	was := phaseTimeLimit
	t.Cleanup(func() { phaseTimeLimit = was })
	phaseTimeLimit = 50 * time.Millisecond
	catalog, _ := newSQLiteCatalog(t)
	work := phaseOf(t, catalog, trickplayContainerName, factProbe)
	probe := newPhaseBoard(work.board.dir)
	if err := probe.start(factProbe); err != nil {
		t.Fatal(err)
	}

	if err := work.runFacts(t.Context(), []string{factTrickplay}); err != nil {
		t.Fatal(err)
	}

	if !work.board.ended(trickplayContainerName).ended {
		t.Error("the phase ended at its limit with no mark")
	}
}

// A phase stopped while it waits ends with the stop and writes no mark, so
// the kubelet's verdict on the container stands.
func TestAStoppedPhaseWritesNoMark(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work := phaseOf(t, catalog, factProbe, scanPhase)
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- work.runFacts(ctx, []string{factProbe}) }()
	time.Sleep(20 * time.Millisecond)

	stop()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("runFacts = %v, want the stop", err)
	}
	if _, marked := work.board.mark(factProbe); marked {
		t.Error("a stopped phase wrote a mark")
	}
}

// A phases volume the container cannot write is an error before any work.
func TestAPhaseThatCannotTakeItsLockFails(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work := phaseOf(t, catalog, factProbe)
	work.board = newPhaseBoard(filepath.Join(t.TempDir(), "absent"))

	if err := work.runFacts(t.Context(), []string{factProbe}); err == nil {
		t.Error("runFacts = nil error with no phases volume to lock on")
	}
}
