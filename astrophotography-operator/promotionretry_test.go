package main

// When the promotion loop tries again the frames that a Job left in the
// outbox: after a new frame, or after a person asks with the retry
// annotation, and never because the operator restarted.

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/astrophotography-operator/promote"
	"github.com/liken-sh/liken/kubernetes/events"
)

// nameConflict is the report of a Job that left its one frame in the
// outbox, because the archive holds another file of the frame's name.
func nameConflict() *promote.Report {
	return &promote.Report{Totals: luminance(0), Left: 1, Failures: []promote.Frame{{
		File: "east-main/east-tonight/m42/M42_0001.fits", Reason: promote.ReasonNameConflict,
		Message: "astro/M42_0001.fits in the archive is another file"}}}
}

// failOneFrame runs an operator until ctx ends, and ends m42's first
// Job with a report that left its one frame in the outbox.
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
			t.Error("no Job started for the new frame")
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

// retryEvents answers the message of each PromotionRetry Event about
// m42.
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
// left, posts PromotionRetry, and is removed. It asks once, so a frame
// that fails again waits for a new frame or the next annotation.
func TestTheRetryAnnotationStartsAJobForTheFramesLeft(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		failOneFrame(t.Context(), t, api)
		annotateRetry(t, api)
		synctest.Wait()

		if job := decode[jobObject](api, jobsPath, observatoryNS, "m42-promote-2"); job.Metadata.Annotations[annotationPending] != "1" {
			t.Errorf("the retry started %+v", job.Metadata)
		}
		target := readTarget(api)
		if _, kept := target.Metadata.Annotations[annotationRetry]; kept || target.Status.Outboxes[0].Left != 0 {
			t.Errorf("the target holds %v and %+v", target.Metadata.Annotations, target.Status.Outboxes)
		}
		want := []string{"the annotation astrophotography.liken.sh/retry asked for a retry, " +
			"so the next promotion Job tries the 1 frame that the last Job left in the outbox again"}
		if got := retryEvents(api); !slices.Equal(got, want) {
			t.Errorf("the PromotionRetry Events are %q, want %q", got, want)
		}

		finish(t, api, "m42-promote-2", nameConflict())
		synctest.Wait()
		if api.get(jobsPath, observatoryNS, "m42-promote-3") != nil {
			t.Error("one annotation started a second retry")
		}
	})
}

// An annotation that arrives while a Job runs waits for the Job to end,
// so a Job that started before the person's fix does not use it up.
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
			t.Error("the retry did not start a Job after the running Job ended")
		}
	})
}

// An annotation on a target with no frame left starts no Job and posts
// no Event, and the operator removes it.
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
			t.Error("the annotation stayed")
		}
		if got := retryEvents(api); len(got) != 0 || api.get(jobsPath, observatoryNS, "m42-promote-1") != nil {
			t.Errorf("the retry posted %q or started a Job", got)
		}
	})
}
