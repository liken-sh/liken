package main

// These tests cover the desk that folds a bus report into the newest
// report per Library, wakes the reconcile loop, and forgets a Library
// the pass no longer sees, all proved with no bus and no cluster.

import (
	"testing"
	"time"
)

// reportWakeTimeout is how long a test waits for a wake before it
// calls the loop unwoken.
const reportWakeTimeout = time.Second

// reportDesk builds a desk on a wake channel one deep, because that is
// the channel the operator's loop hands the real one.
func reportDesk(t *testing.T) (*reports, chan struct{}) {
	t.Helper()
	wake := make(chan struct{}, 1)
	return newReports(wake), wake
}

// walkedReport is one scanner's observation of a volume it has walked
// through to the end.
func walkedReport() libraryReport {
	return libraryReport{
		Titles:       412,
		Unidentified: 3,
		LastWalk:     time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC),
		LastChange:   time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC),
	}
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

	desk.fold("house", "movies", walkedReport())

	stored := desk.latestFor("house", "movies")
	if stored == nil {
		t.Fatal("the desk holds no report for house/movies")
	}
	if stored.Titles != 412 || stored.Unidentified != 3 {
		t.Errorf("stored report = %+v", *stored)
	}
	if !stored.LastWalk.Equal(walkedReport().LastWalk) {
		t.Errorf("last walk = %v, want %v", stored.LastWalk, walkedReport().LastWalk)
	}
	waitForReportWake(t, wake)
}

// A report is a whole observation, so the newest one is the one the
// desk holds and the older one says nothing more.
func TestTheNewestReportIsTheOneTheDeskHolds(t *testing.T) {
	desk, wake := reportDesk(t)

	desk.fold("house", "movies", walkedReport())
	later := walkedReport()
	later.Titles = 413
	later.Unidentified = 0
	desk.fold("house", "movies", later)

	stored := desk.latestFor("house", "movies")
	if stored == nil {
		t.Fatal("the desk holds no report for house/movies")
	}
	if stored.Titles != 413 || stored.Unidentified != 0 {
		t.Errorf("stored report = %+v, want the second one", *stored)
	}
	// Two folds leave one wake, because the pass that answers it reads
	// the whole collection.
	waitForReportWake(t, wake)
	expectNoReportWake(t, wake)
}

// A Library the desk has taken no message for has no report, which is
// how the operator tells a scanner that has not walked yet from one
// that reports an empty volume.
func TestTheDeskHoldsNoReportForALibraryItHasNotHeardFrom(t *testing.T) {
	desk, _ := reportDesk(t)

	if stored := desk.latestFor("house", "movies"); stored != nil {
		t.Errorf("report = %+v, want none", *stored)
	}
}

// A scanner that goes offline keeps the report it published: the
// counts describe the volume and not the pod, so the last walk stands
// until the next walk replaces it.
func TestAnOfflineScannerKeepsItsReport(t *testing.T) {
	desk, wake := reportDesk(t)
	desk.fold("house", "movies", walkedReport())
	waitForReportWake(t, wake)

	desk.availability("house", "movies", false)

	stored := desk.latestFor("house", "movies")
	if stored == nil {
		t.Fatal("the offline scanner's report is gone")
	}
	if stored.Titles != 412 {
		t.Errorf("stored report = %+v, want the last walk's counts", *stored)
	}
	waitForReportWake(t, wake)
}

// The first signal for a Library is a change, and so is every flip of
// the flag after it. A repeat of the flag wakes nothing, because a
// reconnecting scanner republishes online on every session.
func TestAvailabilityWakesOnEveryChangeAndOnNoRepeat(t *testing.T) {
	desk, wake := reportDesk(t)

	desk.availability("house", "movies", true)
	waitForReportWake(t, wake)

	desk.availability("house", "movies", true)
	expectNoReportWake(t, wake)

	desk.availability("house", "movies", false)
	waitForReportWake(t, wake)
}

// The desk reports the last availability the bus carried for a scanner,
// the fact the phase reads to tell Offline from Idle.
func TestTheDeskAnswersWhetherAScannerIsOnline(t *testing.T) {
	desk, _ := reportDesk(t)

	if desk.onlineFor("house", "movies") {
		t.Error("a Library the desk holds no availability for reads online")
	}

	desk.availability("house", "movies", true)
	if !desk.onlineFor("house", "movies") {
		t.Error("the desk reads an online scanner as offline")
	}

	desk.availability("house", "movies", false)
	if desk.onlineFor("house", "movies") {
		t.Error("the desk reads a scanner that left as online")
	}
}

// The pass hands over the Libraries that still exist, and the desk
// shrinks to match, so a Library created later under the same name
// starts with no report and no availability.
func TestTheDeskForgetsALibraryThatIsGoneAndKeepsTheOneThatIsNot(t *testing.T) {
	desk, wake := reportDesk(t)
	desk.fold("house", "movies", walkedReport())
	desk.fold("attic", "series", walkedReport())
	desk.availability("attic", "series", true)
	waitForReportWake(t, wake)

	desk.retain(map[string]bool{libraryKey("house", "movies"): true})

	if desk.latestFor("house", "movies") == nil {
		t.Error("the live Library's report is gone")
	}
	if stored := desk.latestFor("attic", "series"); stored != nil {
		t.Errorf("the gone Library's report = %+v, want none", *stored)
	}
	// The availability the desk held for the gone Library went with
	// it, so the same signal reads as a change again.
	desk.availability("attic", "series", true)
	waitForReportWake(t, wake)
}

func TestALibraryKeyNamesTheNamespaceAndTheName(t *testing.T) {
	if got := libraryKey("house", "movies"); got != "house/movies" {
		t.Errorf("key = %q, want house/movies", got)
	}
}
