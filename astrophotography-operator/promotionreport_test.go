package main

// The promotion loop against the fake API server: how a Job's report
// changes the Target's status, and the finalizer on the outbox claims.

import (
	"reflect"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/catalog"
	"github.com/liken-sh/liken/astrophotography-operator/promote"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func luminance(frames int32) []catalog.Total {
	return []catalog.Total{{Settings: catalog.Settings{Filter: "Luminance", ExposureSeconds: 300, Binning: 1}, Frames: frames}}
}

// A report sets archived from the catalog's totals and subtracts the
// frames it promoted from pending. Frames counted after the Job started
// stay pending, and they start the next Job.
func TestAReportUpdatesTheCounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putClaim(api, "frames")
		putClaim(api, "archive")
		putTarget(t, api, withPending(m42(), 2, 0))
		startOperator(t, api)
		synctest.Wait()
		// The runner counts a third frame while the Job runs.
		putTarget(t, api, withPending(readTarget(api), 3, 0))
		synctest.Wait()

		finish(t, api, "m42-promote-1", &promote.Report{Totals: luminance(2), Promoted: 2, Bytes: 11520})
		synctest.Wait()

		status := readTarget(api).Status
		wantSequences := []astrophotography.SequenceProgress{{Sequence: m42().Spec.Sequences[0], Archived: 2, Pending: 1}}
		wantOutboxes := []astrophotography.OutboxFrames{{ClaimName: "frames", Pending: 1}}
		if !reflect.DeepEqual(status.Sequences, wantSequences) || !reflect.DeepEqual(status.Outboxes, wantOutboxes) ||
			status.Progress != "2/3" || status.Promotion == nil || status.Promotion.Applied != 1 {
			t.Errorf("the status is %+v, want 2 archived, 1 pending, progress 2/3, and Applied 1", status)
		}
		complete, _ := conditions.Find(status.Conditions, astrophotography.ConditionComplete)
		available, _ := conditions.Find(status.Conditions, astrophotography.ConditionArchiveAvailable)
		if complete.Reason != astrophotography.ReasonWorkLeft || available.Reason != astrophotography.ReasonPromoted {
			t.Errorf("Complete is %+v and ArchiveAvailable is %+v, want the reasons %s and %s", complete, available, astrophotography.ReasonWorkLeft, astrophotography.ReasonPromoted)
		}
		next := decode[jobObject](api, jobsPath, observatoryNS, "m42-promote-2")
		if next.Metadata.Annotations[annotationPending] != "1" {
			t.Errorf("the next Job has the annotations %v, want %s to be 1", next.Metadata.Annotations, annotationPending)
		}
	})
}

// Archiving the last frames completes the Target and removes the
// outbox's entry from the status, which removes the finalizer from the
// claim.
func TestTheLastFramesCompleteTheTarget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putClaim(api, "frames")
		putClaim(api, "archive")
		putTarget(t, api, withPending(m42(), 3, 0))
		startOperator(t, api)
		synctest.Wait()

		finish(t, api, "m42-promote-1", &promote.Report{Totals: luminance(3), Promoted: 3})
		synctest.Wait()

		status := readTarget(api).Status
		complete, _ := conditions.Find(status.Conditions, astrophotography.ConditionComplete)
		if complete.Status != conditions.True || complete.Reason != astrophotography.ReasonArchived || len(status.Outboxes) != 0 {
			t.Errorf("the status is %+v, want Complete True with the reason %s and no outboxes", status, astrophotography.ReasonArchived)
		}
		claim := decode[claimObject](api, claimsPath, observatoryNS, "frames")
		if len(claim.Metadata.Finalizers) != 0 {
			t.Errorf("the claim has the finalizers %v, want none", claim.Metadata.Finalizers)
		}
		if api.get(jobsPath, observatoryNS, "m42-promote-2") != nil {
			t.Error("a second Job started, want none because no frame is pending")
		}
	})
}

// A frame that failed to promote waits for a person. The Job's failure
// is reported in ArchiveAvailable, and no Job starts again until a new
// frame arrives.
func TestAFailedFrameStartsNoJobUntilANewFrame(t *testing.T) {
	cases := []struct {
		name    string
		report  *promote.Report
		reason  string
		message string
	}{
		{"a name conflict", &promote.Report{Totals: luminance(0), Left: 1,
			Failures: []promote.Frame{{File: "east-main/east-tonight/m42/M42_0001.fits", Reason: promote.ReasonNameConflict,
				Message: "the archive already has a different file at astro/M42_0001.fits"}}},
			astrophotography.ReasonNameConflict,
			"The promotion Job m42-promote-1 left 1 frame in the outbox, and the first failure is " +
				"east-main/east-tonight/m42/M42_0001.fits: the archive already has a different file at astro/M42_0001.fits"},
		{"a Job that failed with no report", nil, astrophotography.ReasonCopyFailed,
			"The promotion Job m42-promote-1 failed: Job has reached the specified backoff limit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				api := startFakeAPI(t)
				putClaim(api, "frames")
				putClaim(api, "archive")
				putTarget(t, api, withPending(m42(), 1, 0))
				startOperator(t, api)
				synctest.Wait()

				finish(t, api, "m42-promote-1", c.report)
				synctest.Wait()

				status := readTarget(api).Status
				available, _ := conditions.Find(status.Conditions, astrophotography.ConditionArchiveAvailable)
				if available.Reason != c.reason || available.Message != c.message {
					t.Errorf("ArchiveAvailable is %+v, want the reason %q and the message %q", available, c.reason, c.message)
				}
				if api.get(jobsPath, observatoryNS, "m42-promote-2") != nil {
					t.Fatal("a Job started again for the failed frame, want none until a new frame is pending")
				}

				putTarget(t, api, withPending(readTarget(api), 2, 1))
				synctest.Wait()

				if api.get(jobsPath, observatoryNS, "m42-promote-2") == nil {
					t.Error("no Job started for the new frame, want one")
				}
			})
		})
	}
}

// The finalizer is on each outbox claim that a Target names, and on no
// other claim. A finalizer that another controller added is not
// removed.
func TestTheFinalizerFollowsTheOutboxes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putClaim(api, "frames")
		putClaim(api, "archive")
		putClaim(api, "old-frames", "kubernetes.io/pvc-protection", framesFinalizer)
		target := withPending(m42(), 1, 0)
		target.Spec.Archive.Promotion = astrophotography.PromotionAfterReservation
		putTarget(t, api, target)
		api.put(reservationsPath, reservation(observatory.ReservationReady))
		startOperator(t, api)
		synctest.Wait()

		finalizers := func(name string) []string {
			return decode[claimObject](api, claimsPath, observatoryNS, name).Metadata.Finalizers
		}
		if got := finalizers("frames"); !reflect.DeepEqual(got, []string{framesFinalizer}) {
			t.Errorf("frames has the finalizers %v, want %v", got, []string{framesFinalizer})
		}
		if got := finalizers("old-frames"); !reflect.DeepEqual(got, []string{"kubernetes.io/pvc-protection"}) {
			t.Errorf("old-frames has the finalizers %v, want only kubernetes.io/pvc-protection", got)
		}
		if got := finalizers("archive"); len(got) != 0 {
			t.Errorf("archive has the finalizers %v, want none", got)
		}
	})
}

// A Job that succeeded but whose pod is gone has no report, so the loop
// deletes the Job and creates it again. The new Job reads the whole
// catalog, so its report has the counts that the lost report had.
func TestAJobWhosePodIsGoneRunsAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putClaim(api, "frames")
		putClaim(api, "archive")
		putTarget(t, api, withPending(m42(), 3, 0))
		startOperator(t, api)
		synctest.Wait()

		complete(t, api, "m42-promote-1")
		synctest.Wait()

		deleted := "DELETE /apis/batch/v1/namespaces/observatory/jobs/m42-promote-1"
		if got := api.writes(jobsPath); !slices.Contains(got, deleted) {
			t.Errorf("the Job writes are %v, want %q among them", got, deleted)
		}
		if again := decode[jobObject](api, jobsPath, observatoryNS, "m42-promote-1"); len(again.Status.Conditions) != 0 {
			t.Errorf("the Job created again has the status %+v, want no conditions", again)
		}
		if got, want := targetEvents(api), []string{"Normal WorkLeft", "Normal " + reasonReportLost}; !slices.Equal(got, want) {
			t.Errorf("the Events are %q, want %q", got, want)
		}

		finish(t, api, "m42-promote-1", &promote.Report{Totals: luminance(3)})
		synctest.Wait()

		status := readTarget(api).Status
		if status.Progress != "3/3" || status.Promotion == nil || status.Promotion.Applied != 1 {
			t.Errorf("the status is %+v, want progress 3/3 and Applied 1", status)
		}
	})
}

// The TTL controller deletes a finished Job with foreground deletion,
// so the pod is deleted before the Job. The loop sends no second delete
// and posts no Event for a Job that is already being deleted.
func TestAJobBeingDeletedIsNotDeletedAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putClaim(api, "frames")
		putClaim(api, "archive")
		putTarget(t, api, withPending(m42(), 3, 0))
		startOperator(t, api)
		synctest.Wait()

		deleting := api.get(jobsPath, observatoryNS, "m42-promote-1")
		deleting["metadata"].(map[string]any)["deletionTimestamp"] = "2026-10-13T02:00:00Z"
		api.put(jobsPath, deleting)
		complete(t, api, "m42-promote-1")
		synctest.Wait()

		deleted := "DELETE /apis/batch/v1/namespaces/observatory/jobs/m42-promote-1"
		if got := api.writes(jobsPath); slices.Contains(got, deleted) {
			t.Errorf("the Job writes are %v, want no %q", got, deleted)
		}
		if got, want := targetEvents(api), []string{"Normal WorkLeft"}; !slices.Equal(got, want) {
			t.Errorf("the Events are %q, want %q", got, want)
		}
	})
}
