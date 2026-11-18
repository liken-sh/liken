package main

import (
	"testing"
	"testing/synctest"
	"time"
)

const reportWakeTimeout = time.Second

// newTestReports returns a reports store and its wake channel, which
// has room for one value like the loop's real channel.
func newTestReports(t *testing.T) (*reports, chan struct{}) {
	t.Helper()
	wake := make(chan struct{}, 1)
	return newReports(wake), wake
}

func runningReport() playReport {
	return playReport{Item: 1, Position: "0:01:00", Duration: "1:30:00"}
}

func waitForReportWake(t *testing.T, wake <-chan struct{}) {
	t.Helper()
	select {
	case <-wake:
	case <-time.After(reportWakeTimeout):
		t.Fatalf("no wake reached the loop within %v, want one", reportWakeTimeout)
	}
}

// expectNoReportWake fails if a wake is queued. It does not wait,
// because fold and availability send their wake before they return.
func expectNoReportWake(t *testing.T, wake <-chan struct{}) {
	t.Helper()
	select {
	case <-wake:
		t.Fatal("a wake reached the loop, want none")
	default:
	}
}

func TestFoldStoresTheReportAndWakesTheLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store, wake := newTestReports(t)

		store.fold("house", "movie", runningReport())

		stored := store.latestFor("house", "movie")
		if stored == nil {
			t.Fatal("the store has no report for house/movie")
		}
		if stored.Item != 1 || stored.Position != "0:01:00" || stored.Duration != "1:30:00" {
			t.Errorf("stored report = %+v, want Item 1, Position 0:01:00, Duration 1:30:00", *stored)
		}
		waitForReportWake(t, wake)
	})
}

// A report that only advances the position is stored without a wake.
// Otherwise a bus that reports every second would write the Play every
// second.
func TestFoldOnAPositionAdvanceStoresButDoesNotWake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store, wake := newTestReports(t)
		store.fold("house", "movie", runningReport())
		waitForReportWake(t, wake)

		advanced := runningReport()
		advanced.Position = "0:01:05"
		store.fold("house", "movie", advanced)

		stored := store.latestFor("house", "movie")
		if stored == nil || stored.Position != "0:01:05" {
			t.Errorf("stored report = %+v, want the advanced position", stored)
		}
		expectNoReportWake(t, wake)
	})
}

// A report that changes Paused wakes the loop.
func TestFoldOnAPauseChangeWakes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store, wake := newTestReports(t)
		store.fold("house", "movie", runningReport())
		waitForReportWake(t, wake)

		paused := runningReport()
		paused.Paused = true
		store.fold("house", "movie", paused)

		waitForReportWake(t, wake)
	})
}

// A report with Ended set wakes the loop and records the ended mark.
func TestFoldOnTheEndingRecordsTheMarkAndWakes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store, wake := newTestReports(t)
		store.fold("house", "movie", runningReport())
		waitForReportWake(t, wake)

		ended := runningReport()
		ended.Ended = true
		store.fold("house", "movie", ended)

		mustMatch(t, store.endedFor("house", "movie"), true)
		waitForReportWake(t, wake)
	})
}

// The sidecar publishes the ending and then goes offline, which deletes
// the latest report. The ended mark stays, because the pod keeps running
// for a few seconds and the Player must read as idle during that time.
func TestTheEndedMarkSurvivesAnOfflineMessage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store, wake := newTestReports(t)
		ended := runningReport()
		ended.Ended = true
		store.fold("house", "movie", ended)
		waitForReportWake(t, wake)

		store.availability("house", "movie", false, "")

		if stored := store.latestFor("house", "movie"); stored != nil {
			t.Errorf("the offline run's report = %+v, want none", *stored)
		}
		mustMatch(t, store.endedFor("house", "movie"), true)
	})
}

// A report with Ended false clears the mark, so a Play recreated under
// the same name does not start ended.
func TestAReportWithoutEndedClearsTheMark(t *testing.T) {
	store, _ := newTestReports(t)
	ended := runningReport()
	ended.Ended = true
	store.fold("house", "movie", ended)

	store.fold("house", "movie", runningReport())

	mustMatch(t, store.endedFor("house", "movie"), false)
}

// retain and forget delete the ended mark of a run, so a later Play
// under the same name does not start ended.
func TestRetainAndForgetDeleteTheEndedMark(t *testing.T) {
	store, _ := newTestReports(t)
	ended := runningReport()
	ended.Ended = true
	store.fold("house", "movie", ended)
	store.fold("attic", "radio", ended)

	store.retain(map[string]bool{runKey("attic", "radio"): true})
	mustMatch(t, store.endedFor("house", "movie"), false)
	mustMatch(t, store.endedFor("attic", "radio"), true)

	store.forget(runKey("attic", "radio"))
	mustMatch(t, store.endedFor("attic", "radio"), false)
}

func TestFoldReplacesThePreviousReport(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store, wake := newTestReports(t)

		store.fold("house", "movie", runningReport())
		later := playReport{Item: 2, Paused: true, Position: "0:45:00", Duration: "1:30:00"}
		store.fold("house", "movie", later)

		stored := store.latestFor("house", "movie")
		if stored == nil {
			t.Fatal("the store has no report for house/movie")
		}
		if stored.Item != 2 || stored.Position != "0:45:00" || !stored.Paused {
			t.Errorf("stored report = %+v, want the second one", *stored)
		}
		// The channel has room for one wake, and the pass it wakes reads
		// the newest report.
		waitForReportWake(t, wake)
		expectNoReportWake(t, wake)
	})
}

// An offline message deletes the retained report, so a status that the
// broker still holds does not read as a live Play.
func TestAvailabilityOfflineDropsTheReportAndWakes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store, wake := newTestReports(t)
		store.fold("house", "movie", runningReport())
		waitForReportWake(t, wake)

		store.availability("house", "movie", false, "")

		if stored := store.latestFor("house", "movie"); stored != nil {
			t.Errorf("the offline run's report = %+v, want none", *stored)
		}
		waitForReportWake(t, wake)
	})
}

// An online message keeps the report and wakes nothing, because the
// next report refreshes the status.
func TestAvailabilityOnlineKeepsTheReportAndDoesNotWake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store, wake := newTestReports(t)
		store.fold("house", "movie", runningReport())
		waitForReportWake(t, wake)

		store.availability("house", "movie", true, "")

		if store.latestFor("house", "movie") == nil {
			t.Error("the run has no report after an online message, want it kept")
		}
		expectNoReportWake(t, wake)
	})
}

func TestRetainDeletesTheReportOfARunThatHasNoPlay(t *testing.T) {
	store, _ := newTestReports(t)
	store.fold("house", "movie", runningReport())
	store.fold("attic", "radio", playReport{Item: 1, Position: "0:02:00"})

	store.retain(map[string]bool{runKey("house", "movie"): true})

	if store.latestFor("house", "movie") == nil {
		t.Error("the report of the run that has a Play is gone after retain, want it kept")
	}
	if stored := store.latestFor("attic", "radio"); stored != nil {
		t.Errorf("the gone run's report = %+v, want none", *stored)
	}
}

// stale returns every run that sent a message and is not in the live
// set, whether its last message was online or offline. forget removes a
// run from the result.
func TestStaleReturnsSeenRunsUntilForgotten(t *testing.T) {
	store, _ := newTestReports(t)
	store.fold("house", "movie", runningReport())
	store.availability("attic", "radio", false, "")

	stale := store.stale(map[string]bool{runKey("house", "movie"): true})
	if len(stale) != 1 || stale[0] != runKey("attic", "radio") {
		t.Fatalf("stale = %v, want [attic/radio]", stale)
	}

	store.forget(runKey("attic", "radio"))
	if got := store.stale(map[string]bool{}); len(got) != 1 || got[0] != runKey("house", "movie") {
		t.Errorf("after forget, stale = %v, want [house/movie]", got)
	}
}
