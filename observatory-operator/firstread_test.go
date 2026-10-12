package main

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// A new copy of the operator that is connected to the site's server
// but has not read the weather station yet leaves the station's stored
// Safe condition alone. Safe keeps its transition time across the
// restart, so the mount's trigger, which runs once for each transition,
// runs once.
func TestANewOperatorKeepsAConditionItHasNotRead(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.hold("Telescope Simulator", "TELESCOPE_PARK")
		w.weather("Alert")
		w.until(time.Minute, "the mount's run is not recorded", func() bool {
			return lastRun(t, w, observatory.MountKind, "east", "triggers[0]").State == observatory.StepRunning
		})
		before := w.condition(observatory.WeatherStationKind, "lab", observatory.ConditionSafe)
		w.halt()
		w.indi.release("Telescope Simulator", "TELESCOPE_PARK")
		w.indi.answerLater("lab-observatory")
		dialed := w.indi.clients("lab-observatory")
		w.start()
		w.until(time.Minute, "the new copy does not connect to the site's server", func() bool {
			return w.indi.clients("lab-observatory") > dialed
		})
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		w.indi.answerLate("lab-observatory")
		w.runEnds(observatory.MountKind, "east", "triggers[0]", 15*time.Minute)
		time.Sleep(time.Minute)
		synctest.Wait()
		if after := w.condition(observatory.WeatherStationKind, "lab", observatory.ConditionSafe); after != before {
			t.Errorf("Safe = %s, want %s", after, before)
		}
		if n := startedRuns(w.api, observatory.MountKind, "east", "triggers[0]"); n != 1 {
			t.Errorf("the mount's run started %d times, want once", n)
		}
	})
}

// A driver that never answers the new copy's read holds the statuses
// until readLimit, and the writer then writes what the operator reads.
func TestANewOperatorWritesStatusesAfterReadLimit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.halt()
		w.indi.silence("Weather Simulator")
		w.start()
		started := time.Now()
		w.weather("Alert")
		w.until(2*readLimit, "the new copy does not write the weather station's Safe", func() bool {
			return strings.HasPrefix(w.condition(observatory.WeatherStationKind, "lab", observatory.ConditionSafe), "False")
		})
		if waited := time.Since(started); waited < readLimit {
			t.Errorf("the new copy wrote the status after %s, want after readLimit", waited)
		}
	})
}

// condition answers one condition of a device as "<status> since
// <lastTransitionTime>".
func (w *world) condition(kind observatory.Kind, name, conditionType string) string {
	w.t.Helper()
	object, _ := decode[deviceObject](w.t, w.api, kindCollection(kind), name)
	c := conditionOf(object.Status.Conditions, conditionType)
	return string(c.Status) + " since " + c.LastTransitionTime.UTC().Format(time.RFC3339)
}
