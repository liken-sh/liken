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

// the pass hands over the Libraries that still exist, and the desk
// shrinks to match, so a Library created later under the same name
// starts with no report. The keys it dropped come back sorted, because
// the pass clears the topics behind them.
func TestTheDeskForgetsALibraryThatIsGoneAndKeepsTheOneThatIsNot(t *testing.T) {
	desk, wake := reportDesk(t)
	desk.fold("house", "movies", walkedReport())
	desk.fold("attic", "series", walkedReport())
	desk.fold("attic", "photos", walkedReport())
	waitForReportWake(t, wake)

	dropped := desk.retain(map[string]bool{libraryKey("house", "movies"): true})

	if desk.latestFor("house", "movies") == nil {
		t.Error("the live Library's report is gone")
	}
	if stored := desk.latestFor("attic", "series"); stored != nil {
		t.Errorf("the gone Library's report = %+v, want none", *stored)
	}
	want := []string{"attic/photos", "attic/series"}
	if len(dropped) != 2 || dropped[0] != want[0] || dropped[1] != want[1] {
		t.Errorf("dropped = %v, want %v", dropped, want)
	}
}

func TestALibraryKeyNamesTheNamespaceAndTheName(t *testing.T) {
	if got := libraryKey("house", "movies"); got != "house/movies" {
		t.Errorf("key = %q, want house/movies", got)
	}
}
