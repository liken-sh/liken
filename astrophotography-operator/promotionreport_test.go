package main

// The promotion loop against the fake API server: what a Job's report
// does to the Target's status, and the finalizer on the outbox claims.

import (
	"reflect"
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

// A report sets archived from the catalog's totals, takes the frames
// it moved out of pending, and keeps the frames counted after the Job
// started. Those start the next Job.
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
			t.Errorf("the status is %+v", status)
		}
		complete, _ := conditions.Find(status.Conditions, astrophotography.ConditionComplete)
		available, _ := conditions.Find(status.Conditions, astrophotography.ConditionArchiveAvailable)
		if complete.Reason != astrophotography.ReasonWorkLeft || available.Reason != astrophotography.ReasonPromoted {
			t.Errorf("Complete is %+v and ArchiveAvailable is %+v", complete, available)
		}
		next := decode[jobObject](api, jobsPath, observatoryNS, "m42-promote-2")
		if next.Metadata.Annotations[annotationPending] != "1" {
			t.Errorf("the next Job is %+v", next.Metadata)
		}
	})
}

// The last frames archived complete the target, and the outbox's entry
// goes, which takes the finalizer off the claim.
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
			t.Errorf("the status is %+v", status)
		}
		claim := decode[claimObject](api, claimsPath, observatoryNS, "frames")
		if len(claim.Metadata.Finalizers) != 0 {
			t.Errorf("the claim keeps %v", claim.Metadata.Finalizers)
		}
		if api.get(jobsPath, observatoryNS, "m42-promote-2") != nil {
			t.Error("a second Job started with no frame pending")
		}
	})
}

// A frame that failed waits for a person. Its Job's failure is in
// ArchiveAvailable, and no Job starts again until a new frame arrives.
func TestAFailedFrameStartsNoJobUntilANewFrame(t *testing.T) {
	cases := []struct {
		name    string
		report  *promote.Report
		reason  string
		message string
	}{
		{"a name conflict", &promote.Report{Totals: luminance(0), Left: 1,
			Failures: []promote.Frame{{File: "east-main/east-tonight/m42/M42_0001.fits", Reason: promote.ReasonNameConflict,
				Message: "astro/M42_0001.fits in the archive is another file"}}},
			astrophotography.ReasonNameConflict,
			"the promotion Job m42-promote-1 left 1 frame in the outbox; the first failure is " +
				"east-main/east-tonight/m42/M42_0001.fits: astro/M42_0001.fits in the archive is another file"},
		{"a Job that failed with no report", nil, astrophotography.ReasonCopyFailed,
			"the promotion Job m42-promote-1 failed: Job has reached the specified backoff limit"},
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
					t.Errorf("ArchiveAvailable is %+v", available)
				}
				if api.get(jobsPath, observatoryNS, "m42-promote-2") != nil {
					t.Fatal("a Job started again for the failed frame")
				}

				putTarget(t, api, withPending(readTarget(api), 2, 1))
				synctest.Wait()

				if api.get(jobsPath, observatoryNS, "m42-promote-2") == nil {
					t.Error("no Job started for the new frame")
				}
			})
		})
	}
}

// The finalizer is on each outbox claim that a target names, and on
// no other claim. Another controller's finalizer stays.
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
			t.Errorf("frames holds %v", got)
		}
		if got := finalizers("old-frames"); !reflect.DeepEqual(got, []string{"kubernetes.io/pvc-protection"}) {
			t.Errorf("old-frames holds %v", got)
		}
		if got := finalizers("archive"); len(got) != 0 {
			t.Errorf("archive holds %v", got)
		}
	})
}
