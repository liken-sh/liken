package main

// What these tests prove: the cleanup Job's wait after it deletes its
// own run. It ends when a standing pod drops the run's confirmation, or
// when a stream opens on a copy that holds none, and it stands on
// anything else.

import (
	"testing"
	"testing/synctest"
	"time"
)

// One cleanup Job's release wait against a real catalog.
func releasingJob(catalog *Catalog) *releaseWaiter {
	return &releaseWaiter{catalog: catalog, library: "house/movies", job: "cleanup-1",
		released: make(chan struct{})}
}

// Waits on one release for a minute of the bubble's clock and returns
// what ended it.
func releaseResult(t *testing.T, wait *releaseWaiter) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- wait.wait(t.Context(), time.Minute) }()
	return <-done
}

// The drop of the run's confirmation ends the wait, because only a
// standing pod that held the delete drops it.
func TestADroppedConfirmationEndsTheRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, _ := newSQLiteCatalog(t)
		if err := catalog.UpsertConfirmation(t.Context(), "house/movies", workerCleanup, "cleanup-1",
			testConfirmerPod, waitingVersion, time.Unix(30, 0)); err != nil {
			t.Fatal(err)
		}
		wait := releasingJob(catalog)
		done := make(chan error, 1)
		go func() { done <- wait.wait(t.Context(), time.Minute) }()
		synctest.Wait()

		if _, err := catalog.dropOrphanConfirmations(t.Context()); err != nil {
			t.Fatal(err)
		}

		if err := <-done; err != nil {
			t.Errorf("the release ended with %v, want the drop", err)
		}
	})
}

// A stream that opens on a copy with no confirmation of the run ends
// the wait, because the hand-off saw one in this copy, and a pod dropped
// it before the stream opened.
func TestAConfirmationAlreadyGoneEndsTheRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, _ := newSQLiteCatalog(t)

		if err := releaseResult(t, releasingJob(catalog)); err != nil {
			t.Errorf("the release ended with %v, want the empty snapshot", err)
		}
	})
}

// A confirmation that stands holds the wait until its timeout, so a
// Job never exits before a standing pod holds its delete.
func TestAConfirmationThatStandsHoldsTheRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, _ := newSQLiteCatalog(t)
		if err := catalog.UpsertConfirmation(t.Context(), "house/movies", workerCleanup, "cleanup-1",
			testConfirmerPod, waitingVersion, time.Unix(30, 0)); err != nil {
			t.Fatal(err)
		}

		if err := releaseResult(t, releasingJob(catalog)); err == nil {
			t.Error("the release ended with no error, want its timeout")
		}
	})
}

// A confirmation of another library's cleanup is no answer to this
// Job's delete, so its drop leaves the wait standing.
func TestTheDropOfAnotherCleanupLeavesTheReleaseStanding(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, _ := newSQLiteCatalog(t)
		for _, library := range []string{"house/movies", "house/series"} {
			if err := catalog.UpsertConfirmation(t.Context(), library, workerCleanup, "cleanup-1",
				testConfirmerPod, waitingVersion, time.Unix(30, 0)); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := catalog.UpsertRun(t.Context(), "house/movies", libraryRun{
			Worker: workerCleanup, Job: "cleanup-1", Finished: time.Unix(20, 0)}); err != nil {
			t.Fatal(err)
		}
		wait := releasingJob(catalog)
		done := make(chan error, 1)
		go func() { done <- wait.wait(t.Context(), time.Minute) }()
		synctest.Wait()

		if _, err := catalog.dropOrphanConfirmations(t.Context()); err != nil {
			t.Fatal(err)
		}

		if err := <-done; err == nil {
			t.Error("the release ended with no error, want its timeout")
		}
	})
}

// A run that cannot be deleted fails the Job before any wait.
func TestTheReleaseFailsWhereTheRunCannotBeDeleted(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	refusing := refusingRunDeletes(t, catalog)

	err := releaseRun(t.Context(), refusing, "house/movies", "cleanup-1", &syncLog{}, time.Minute)

	if err == nil {
		t.Error("the release returned no error, want the refused delete")
	}
}
