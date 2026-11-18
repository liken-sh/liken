package main

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The dome's driver refuses the park that bad weather triggers, so the
// run fails. A retry while the weather is still unsafe parks the dome.
// A retry after the weather turned safe leaves the run Failed, because
// its transition has ended.
func TestTheRetryAnnotationRerunsAFailedTriggersRun(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		weather string
		state   observatory.StepState
		summary string
		starts  int
	}{
		{"while the condition holds", "Alert", observatory.StepDone, "When WeatherStation lab Safe=False: parked Dome lab", 2},
		{"after the condition changed", "Ok", observatory.StepFailed,
			"When WeatherStation lab Safe=False: state: Parked: Dome lab: indi: Dome Simulator.DOME_PARK is Alert", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := readyWorld(t)
				w.indi.refuse("Dome Simulator", "DOME_PARK")
				w.weather("Alert")
				if run := w.runEnds(observatory.DomeKind, "lab", "triggers[0]", time.Minute); run.State != observatory.StepFailed {
					t.Fatalf("the dome's park = %s %q, want Failed", run.State, run.Summary)
				}
				w.indi.accept("Dome Simulator", "DOME_PARK")
				w.weather(c.weather)
				w.settle()
				w.annotateRetry(observatory.DomeKind, "lab")
				w.until(time.Minute, "the operator does not remove the annotation", func() bool {
					object, _ := w.api.object(kindCollection(observatory.DomeKind), "lab")
					annotations, _ := object["metadata"].(map[string]any)["annotations"].(map[string]any)
					_, still := annotations[annotationRetry]
					return !still
				})
				w.settle()
				run := w.runEnds(observatory.DomeKind, "lab", "triggers[0]", time.Minute)
				if run.State != c.state || run.Summary != c.summary {
					t.Errorf("the dome's park = %s %q, want %s %q", run.State, run.Summary, c.state, c.summary)
				}
				if n := startedRuns(w.api, observatory.DomeKind, "lab", "triggers[0]"); n != c.starts {
					t.Errorf("the dome's park started %d times, want %d", n, c.starts)
				}
			})
		})
	}
}
