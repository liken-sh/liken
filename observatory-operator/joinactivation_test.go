package main

// A device that joins a telescope or an observatory that is Active runs
// its activation, as an answer to the same transition of Active that
// the reservation's Activation step answered.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func TestADeviceThatJoinsAnActiveParentRunsItsActivation(t *testing.T) {
	cases := []struct {
		name               string
		kind               observatory.Kind
		device             string
		governor           observatory.Kind
		governorName, want string
	}{
		{"a dust cap of the telescope", observatory.DustCapKind, "east", observatory.TelescopeKind, "east", "Opened DustCap east"},
		{"the dome of the observatory", observatory.DomeKind, "lab", observatory.ObservatoryKind, "lab", "Unparked Dome lab"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				collection := kindCollection(tc.kind)
				object, _ := w.api.object(collection, tc.device)
				w.api.deleteNamed(collection, tc.device)
				w.indi.preset("Dome Simulator", "DOME_PARK", "PARK")
				w.indi.preset("Dust Cover Simulator", "CAP_PARK", "PARK")
				w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
				w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)

				joined := time.Now()
				w.api.put(collection, map[string]any{
					"apiVersion": object["apiVersion"], "kind": object["kind"],
					"metadata": map[string]any{"name": tc.device},
					"spec":     object["spec"],
				})
				var run observatory.ProcedureRun
				w.until(5*time.Minute, "the joining device's activation is not Done", func() bool {
					runs := deviceRuns(t, w, tc.kind, tc.device)
					if len(runs) == 0 {
						return false
					}
					run = runs[0]
					return run.Trigger == observatory.TriggerActivation && run.State == observatory.StepDone
				})
				active := activeOf(t, w, tc.governor, tc.governorName)
				if run.Since == nil || !run.Since.Equal(active.LastTransitionTime) || run.StartTime.Before(joined) {
					t.Errorf("the run = %s, want one since %v that started after %v", mustJSON(run), active.LastTransitionTime, joined)
				}
				if got := run.Actions[0].Summary; got != tc.want {
					t.Errorf("the action's summary = %q, want %q", got, tc.want)
				}
			})
		})
	}
}

// deviceRuns answers the runs that a device's stored status records.
func deviceRuns(t *testing.T, w *world, kind observatory.Kind, name string) []observatory.ProcedureRun {
	object, _ := decode[struct {
		Status struct{ Procedures []observatory.ProcedureRun }
	}](t, w.api, kindCollection(kind), name)
	return object.Status.Procedures
}

// activeOf answers the stored Active condition of a Telescope or an
// Observatory.
func activeOf(t *testing.T, w *world, kind observatory.Kind, name string) observatory.Condition {
	if kind == observatory.TelescopeKind {
		scope, _ := decode[observatory.Telescope](t, w.api, kindCollection(kind), name)
		return conditionOf(scope.Status.Conditions, observatory.ConditionActive)
	}
	site, _ := decode[observatory.Observatory](t, w.api, kindCollection(kind), name)
	return conditionOf(site.Status.Conditions, observatory.ConditionActive)
}

// A joining device's activation that fails runs again through the
// retry annotation on the device, with its Done actions skipped.
func TestTheRetryAnnotationRerunsAJoiningDevicesFailedActivation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		collection := kindCollection(observatory.DustCapKind)
		object, _ := w.api.object(collection, "east")
		w.api.deleteNamed(collection, "east")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)

		w.indi.preset("Dust Cover Simulator", "CAP_PARK", "PARK")
		w.indi.refuse("Dust Cover Simulator", "CAP_PARK")
		w.put(observatory.DustCapKind, "east", object["spec"].(map[string]any))
		if run := w.runEnds(observatory.DustCapKind, "east", observatory.TriggerActivation, 5*time.Minute); run.State != observatory.StepFailed {
			t.Fatalf("the joining activation = %s %q, want Failed", run.State, run.Summary)
		}
		w.indi.accept("Dust Cover Simulator", "CAP_PARK")
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
