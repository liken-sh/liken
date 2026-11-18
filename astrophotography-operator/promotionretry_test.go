package main

// The promotion loop retries the frames that a Job left in the outbox
// after a new frame arrives or a person adds the retry annotation, and
// never because the operator restarted.

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/astrophotography-operator/promote"
	"github.com/liken-sh/liken/kubernetes/events"
)

// nameConflict is the report of a Job that left one frame in the
// outbox, because the archive has a different file with the frame's
// name.
func nameConflict() *promote.Report {
	return &promote.Report{Totals: luminance(0), Left: 1, Failures: []promote.Frame{{
		File: "east-main/east-tonight/m42/M42_0001.fits", Reason: promote.ReasonNameConflict,
		Message: "the archive already has a different file at astro/M42_0001.fits"}}}
}

// failOneFrame runs an operator until ctx ends, and finishes m42's
// first Job with the nameConflict report.
func failOneFrame(ctx context.Context, t *testing.T, api *fakeAPI) {
	t.Helper()
	putClaim(api, "frames")
	putClaim(api, "archive")
	putTarget(t, api, withPending(m42(), 1, 0))
	api.put(podsPath, operatorPod("operator"))
	runOperatorUntil(ctx, t, api)
	synctest.Wait()
	finish(t, api, "m42-promote-1", nameConflict())
	synctest.Wait()
}

// A frame that failed starts no Job after a restart of the operator,
// because status.outboxes records the frames that the last Job left. A
// new frame starts the next Job.
func TestAFailedFrameStartsNoJobAfterARestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		ctx, stop := context.WithCancel(t.Context())
		failOneFrame(ctx, t, api)
		stop()
		synctest.Wait()

		runOperator(t, api)
		synctest.Wait()
		if api.get(jobsPath, observatoryNS, "m42-promote-2") != nil {
			t.Fatal("the next operator started a Job again for the failed frame")
		}

		putTarget(t, api, withPending(readTarget(api), 2, 1))
		synctest.Wait()
		if api.get(jobsPath, observatoryNS, "m42-promote-2") == nil {
			t.Error("no Job m42-promote-2 started for the new frame")
		}
	})
}

// annotateRetry puts the retry annotation on m42, as
// `kubectl annotate target m42 astrophotography.liken.sh/retry=1` does.
func annotateRetry(t *testing.T, api *fakeAPI) {
	target := readTarget(api)
	target.Metadata.Annotations = map[string]string{annotationRetry: "1"}
	putTarget(t, api, target)
}

func retryEvents(api *fakeAPI) []string {
	var out []string
	for _, e := range api.recorded.About("Target", "m42") {
		if e.Type == events.TypeNormal && e.Reason == reasonPromotionRetry {
			out = append(out, e.Message)
		}
	}
	return out
}

// The retry annotation starts a Job for the frames that the last Job
// left, posts PromotionRetry, and is removed. A frame that fails again
// in that Job starts no third Job.
func TestTheRetryAnnotationStartsAJobForTheFramesLeft(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		failOneFrame(t.Context(), t, api)
		annotateRetry(t, api)
		synctest.Wait()

		if job := decode[jobObject](api, jobsPath, observatoryNS, "m42-promote-2"); job.Metadata.Annotations[annotationPending] != "1" {
			t.Errorf("the retry Job is %+v, want annotation %s = 1", job.Metadata, annotationPending)
		}
		target := readTarget(api)
		if _, kept := target.Metadata.Annotations[annotationRetry]; kept || target.Status.Outboxes[0].Left != 0 {
			t.Errorf("the Target has annotations %v and outboxes %+v, want no retry annotation and no frames left", target.Metadata.Annotations, target.Status.Outboxes)
		}
		if got := retryEvents(api); len(got) != 1 || !strings.Contains(got[0], "the 1 frame that the last Job left") {
			t.Errorf("the PromotionRetry Events are %q, want one that names the 1 frame that the last Job left", got)
		}

		finish(t, api, "m42-promote-2", nameConflict())
		synctest.Wait()
		if api.get(jobsPath, observatoryNS, "m42-promote-3") != nil {
			t.Error("one retry annotation started a second Job m42-promote-3, want none")
		}
	})
}

func TestTheRetryAnnotationWaitsForTheRunningJob(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putClaim(api, "frames")
		putClaim(api, "archive")
		putTarget(t, api, withPending(m42(), 1, 0))
		startOperator(t, api)
		synctest.Wait()
		annotateRetry(t, api)
		synctest.Wait()

		finish(t, api, "m42-promote-1", nameConflict())
		synctest.Wait()

		if api.get(jobsPath, observatoryNS, "m42-promote-2") == nil {
			t.Error("no Job m42-promote-2 started after the running Job ended, want the retry to start one")
		}
	})
}

func TestTheRetryAnnotationWithNoFrameLeftIsRemoved(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putClaim(api, "archive")
		putTarget(t, api, m42())
		startOperator(t, api)
		synctest.Wait()
		annotateRetry(t, api)
		synctest.Wait()

		if _, kept := readTarget(api).Metadata.Annotations[annotationRetry]; kept {
			t.Error("the retry annotation is still on the Target, want it removed")
		}
		if got := retryEvents(api); len(got) != 0 || api.get(jobsPath, observatoryNS, "m42-promote-1") != nil {
			t.Errorf("the retry posted Events %q or started a Job, want neither", got)
		}
	})
}
