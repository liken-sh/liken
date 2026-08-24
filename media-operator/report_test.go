package main

// These tests cover the desk that folds a bus report into the newest
// per-run report, drops a run marked offline, and forgets a run the
// pass no longer sees, all proved with no bus and no cluster.

import (
	"testing"
	"time"
)

// reportWakeTimeout is how long a test waits for a wake before it calls
// the loop unwoken.
const reportWakeTimeout = time.Second

// reportDesk builds a desk on a wake channel one deep, because that is
// the channel the operator's loop hands the real one.
func reportDesk(t *testing.T) (*reports, chan struct{}) {
	t.Helper()
	wake := make(chan struct{}, 1)
	return newReports(wake), wake
}

// runningReport is one observation of a run while the position
// advances.
func runningReport() playReport {
	return playReport{Item: 1, Position: "0:01:00", Duration: "1:30:00"}
}

func waitForReportWake(t *testing.T, wake <-chan struct{}) {
	t.Helper()
	select {
	case <-wake:
	case <-time.After(reportWakeTimeout):
		t.Fatal("no wake reached the loop")
	}
}

// expectNoReportWake needs no waiting: the wake is already there or it
// is not, because fold and availability poke it before they return.
func expectNoReportWake(t *testing.T, wake <-chan struct{}) {
	t.Helper()
	select {
	case <-wake:
		t.Fatal("a wake reached the loop")
	default:
	}
}

func TestFoldStoresTheReportAndWakesTheLoop(t *testing.T) {
	desk, wake := reportDesk(t)

	desk.fold("house", "movie", runningReport())

	stored := desk.latestFor("house", "movie")
	if stored == nil {
		t.Fatal("the desk holds no report for house/movie")
	}
	if stored.Item != 1 || stored.Position != "0:01:00" || stored.Duration != "1:30:00" {
		t.Errorf("stored report = %+v", *stored)
	}
	waitForReportWake(t, wake)
}

// A position that only advances updates the desk but wakes nothing, so a
// one-second bus cadence does not become a one-second write. The bare
// position rides the operator's backstop tick instead.
func TestFoldOnAPositionAdvanceStoresButDoesNotWake(t *testing.T) {
	desk, wake := reportDesk(t)
	desk.fold("house", "movie", runningReport())
	waitForReportWake(t, wake)

	advanced := runningReport()
	advanced.Position = "0:01:05"
	desk.fold("house", "movie", advanced)

	stored := desk.latestFor("house", "movie")
	if stored == nil || stored.Position != "0:01:05" {
		t.Errorf("stored report = %+v, want the advanced position", stored)
	}
	expectNoReportWake(t, wake)
}

// A pause is what a person waits to see, so a report that flips it wakes
// the loop even when the item has not changed.
func TestFoldOnAPauseChangeWakes(t *testing.T) {
	desk, wake := reportDesk(t)
	desk.fold("house", "movie", runningReport())
	waitForReportWake(t, wake)

	paused := runningReport()
	paused.Paused = true
	desk.fold("house", "movie", paused)

	waitForReportWake(t, wake)
}

// The ending is what a person is watching the screen for, so a report
// that carries the mark wakes the loop even though the item and the pause
// stand where they were. The pass it wakes is what turns the idle screen
// back on.
func TestFoldOnTheEndingRecordsTheMarkAndWakes(t *testing.T) {
	desk, wake := reportDesk(t)
	desk.fold("house", "movie", runningReport())
	waitForReportWake(t, wake)

	ended := runningReport()
	ended.Ended = true
	desk.fold("house", "movie", ended)

	mustMatch(t, desk.endedFor("house", "movie"), true)
	waitForReportWake(t, wake)
}

// The sidecar publishes the ending and then goes offline, which drops the
// report. The mark outlives the drop, because the pod lives on for seconds
// after the film is over and the unit is idle for every one of them.
func TestTheEndingOutlivesTheOfflineDrop(t *testing.T) {
	desk, wake := reportDesk(t)
	ended := runningReport()
	ended.Ended = true
	desk.fold("house", "movie", ended)
	waitForReportWake(t, wake)

	desk.availability("house", "movie", false)

	if stored := desk.latestFor("house", "movie"); stored != nil {
		t.Errorf("the offline run's report = %+v, want none", *stored)
	}
	mustMatch(t, desk.endedFor("house", "movie"), true)
}

// A run reports its own ending, so a Play recreated under the same name
// starts not ended.
func TestAFreshReportClearsTheEnding(t *testing.T) {
	desk, _ := reportDesk(t)
	ended := runningReport()
	ended.Ended = true
	desk.fold("house", "movie", ended)

	desk.fold("house", "movie", runningReport())

	mustMatch(t, desk.endedFor("house", "movie"), false)
}

// The ending goes with the run it belongs to, both when the pass drops a
// deleted Play and when the reclaim forgets it, so a later Play under the
// same name does not start life ended.
func TestTheDeskDropsTheEndingWithTheRun(t *testing.T) {
	desk, _ := reportDesk(t)
	ended := runningReport()
	ended.Ended = true
	desk.fold("house", "movie", ended)
	desk.fold("attic", "radio", ended)

	desk.retain(map[string]bool{runKey("attic", "radio"): true})
	mustMatch(t, desk.endedFor("house", "movie"), false)
	mustMatch(t, desk.endedFor("attic", "radio"), true)

	desk.forget(runKey("attic", "radio"))
	mustMatch(t, desk.endedFor("attic", "radio"), false)
}

func TestTheNewestReportIsTheOneTheDeskHolds(t *testing.T) {
	desk, wake := reportDesk(t)

	desk.fold("house", "movie", runningReport())
	later := playReport{Item: 2, Paused: true, Position: "0:45:00", Duration: "1:30:00"}
	desk.fold("house", "movie", later)

	stored := desk.latestFor("house", "movie")
	if stored == nil {
		t.Fatal("the desk holds no report for house/movie")
	}
	if stored.Item != 2 || stored.Position != "0:45:00" || !stored.Paused {
		t.Errorf("stored report = %+v, want the second one", *stored)
	}
	// Two folds leave one wake, because the pass that answers it reads
	// the whole collection.
	waitForReportWake(t, wake)
	expectNoReportWake(t, wake)
}

// A Play marked offline drops its retained report, so a status the
// broker still holds does not read as a live Play.
func TestAvailabilityOfflineDropsTheReportAndWakes(t *testing.T) {
	desk, wake := reportDesk(t)
	desk.fold("house", "movie", runningReport())
	waitForReportWake(t, wake)

	desk.availability("house", "movie", false)

	if stored := desk.latestFor("house", "movie"); stored != nil {
		t.Errorf("the offline run's report = %+v, want none", *stored)
	}
	waitForReportWake(t, wake)
}

// A Play marked online keeps whatever report it has and wakes nothing,
// because the next report refills the status on its own.
func TestAvailabilityOnlineKeepsTheReportAndDoesNotWake(t *testing.T) {
	desk, wake := reportDesk(t)
	desk.fold("house", "movie", runningReport())
	waitForReportWake(t, wake)

	desk.availability("house", "movie", true)

	if desk.latestFor("house", "movie") == nil {
		t.Error("an online signal dropped the report")
	}
	expectNoReportWake(t, wake)
}

func TestTheDeskForgetsARunThatIsGoneAndKeepsTheOneThatIsNot(t *testing.T) {
	desk, _ := reportDesk(t)
	desk.fold("house", "movie", runningReport())
	desk.fold("attic", "radio", playReport{Item: 1, Position: "0:02:00"})

	desk.retain(map[string]bool{runKey("house", "movie"): true})

	if desk.latestFor("house", "movie") == nil {
		t.Error("the live run's report is gone")
	}
	if stored := desk.latestFor("attic", "radio"); stored != nil {
		t.Errorf("the gone run's report = %+v, want none", *stored)
	}
}

// The desk remembers every run it has taken a message for, online or
// offline, so the operator can find a deleted Play's retained topics.
// stale returns the seen runs the live set no longer holds, and forget
// drops one for good.
func TestTheDeskReportsSeenRunsAsStaleUntilForgotten(t *testing.T) {
	desk, _ := reportDesk(t)
	desk.fold("house", "movie", runningReport())
	desk.availability("attic", "radio", false)

	stale := desk.stale(map[string]bool{runKey("house", "movie"): true})
	if len(stale) != 1 || stale[0] != runKey("attic", "radio") {
		t.Fatalf("stale = %v, want [attic/radio]", stale)
	}

	desk.forget(runKey("attic", "radio"))
	if got := desk.stale(map[string]bool{}); len(got) != 1 || got[0] != runKey("house", "movie") {
		t.Errorf("after forget, stale = %v, want [house/movie]", got)
	}
}
