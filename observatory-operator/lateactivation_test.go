package main

// A device whose activation runs after its parent's Activation step,
// because its interface arrived late and it healed, answers the same
// transition of Active. served_test.go holds the heal itself.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// A late activation that fails runs again through the retry annotation
// on the device, with its Done actions skipped.
func TestTheRetryAnnotationRerunsALateActivationThatFailed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.flipFlat()
		w.indi.withhold("Flip-Flat Simulator", "CAP_PARK")
		readyOf(w)

		w.indi.refuse("Flip-Flat Simulator", "CAP_PARK")
		w.indi.reveal("Flip-Flat Simulator", "CAP_PARK")
		if run := w.runEnds(observatory.DustCapKind, "east", observatory.TriggerActivation, 5*time.Minute); run.State != observatory.StepFailed {
			t.Fatalf("the late activation = %s %q, want Failed", run.State, run.Summary)
		}
		w.indi.accept("Flip-Flat Simulator", "CAP_PARK")
		w.annotateRetry(observatory.DustCapKind, "east")
		var run observatory.ProcedureRun
		w.until(5*time.Minute, "the retried activation is not Done", func() bool {
			run = lastRun(t, w, observatory.DustCapKind, "east", observatory.TriggerActivation)
			return run.State == observatory.StepDone
		})
		if got := run.Actions[0].Summary; got != "Opened DustCap east" {
			t.Errorf("the action's summary = %q, want %q", got, "Opened DustCap east")
		}
		if n := startedRuns(w.api, observatory.DustCapKind, "east", observatory.TriggerActivation); n != 2 {
			t.Errorf("the activation started %d times, want 2", n)
		}
	})
}
