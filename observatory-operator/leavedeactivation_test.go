package main

// A device that leaves a telescope or an observatory that is Active,
// while its object still exists, runs its deactivation before its
// driver stops. A device whose object a person deletes stops with no
// deactivation: its spec is gone.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// moveDevice replaces a device's parent field, or removes it when
// parent is "", and keeps the rest of its spec.
func (w *world) moveDevice(kind observatory.Kind, name, field, parent string) {
	object, _ := w.api.object(kindCollection(kind), name)
	spec := object["spec"].(map[string]any)
	for _, f := range []string{"observatory", "telescope", "opticalTrain"} {
		delete(spec, f)
	}
	if parent != "" {
		spec[field] = parent
	}
	w.put(kind, name, spec)
}

func TestADeviceThatLeavesAnActiveParentRunsItsDeactivation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		kind          observatory.Kind
		device, pod   string
		field, parent string
		// parkMount parks the mount first, because the dome's lock
		// refuses a park while a mount is unparked.
		parkMount bool
		sent      string
	}{
		{"a dust cap put on the shelf", observatory.DustCapKind, "east", "east-dustcap", "", "", false,
			"east-telescope Dust Cover Simulator.CAP_PARK PARK=On UNPARK=Off"},
		{"a dust cap moved to another telescope", observatory.DustCapKind, "east", "east-dustcap", "opticalTrain", "west-imaging", false,
			"east-telescope Dust Cover Simulator.CAP_PARK PARK=On UNPARK=Off"},
		{"a dome put on the shelf", observatory.DomeKind, "lab", "lab-dome", "", "", true,
			"lab-observatory Dome Simulator.DOME_PARK PARK=On UNPARK=Off"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := readyWorld(t)
				if c.parkMount {
					w.indi.ask("east-telescope", "Telescope Simulator", "TELESCOPE_PARK", "PARK=On", "UNPARK=Off")
					w.settle()
				}
				w.moveDevice(c.kind, c.device, c.field, c.parent)
				run := w.runEnds(c.kind, c.device, observatory.TriggerDeactivation, 5*time.Minute)
				if run.State != observatory.StepDone {
					t.Errorf("the deactivation = %s %q, want Done", run.State, run.Summary)
				}
				if !slices.Contains(w.indi.changes(), c.sent) {
					t.Errorf("changes = %q, want %q", w.indi.changes(), c.sent)
				}
				w.until(5*time.Minute, "the device's pod stays", func() bool {
					return !slices.Contains(w.api.names(podsCollection), c.pod)
				})
			})
		})
	}
}

// A deactivation that fails posts ProcedureFailed, and the driver
// stops anyway.
func TestALeavingDevicesFailedDeactivationStillStopsItsDriver(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.refuse("Dust Cover Simulator", "CAP_PARK")
		w.moveDevice(observatory.DustCapKind, "east", "", "")
		run := w.runEnds(observatory.DustCapKind, "east", observatory.TriggerDeactivation, 5*time.Minute)
		want := "state: Closed: DustCap east: indi: Dust Cover Simulator.CAP_PARK is Alert"
		if run.State != observatory.StepFailed || run.Summary != want {
			t.Errorf("the deactivation = %s %q, want Failed %q", run.State, run.Summary, want)
		}
		w.until(5*time.Minute, "the dust cap's pod stays", func() bool {
			return !slices.Contains(w.api.names(podsCollection), "east-dustcap")
		})
		failed := "Warning ProcedureFailed: Procedure deactivation failed: " + want
		if got := typedEvents(w.api, observatory.DustCapKind, "east"); !slices.Contains(got, failed) {
			t.Errorf("dust cap Events = %q, want %q", got, failed)
		}
	})
}

// A deleted device has no spec left to run, so its driver stops with
// no deactivation.
func TestADeletedDeviceStopsWithNoDeactivation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.api.deleteNamed(kindCollection(observatory.DustCapKind), "east")
		w.until(5*time.Minute, "the dust cap's pod stays", func() bool {
			return !slices.Contains(w.api.names(podsCollection), "east-dustcap")
		})
		if n := startedRuns(w.api, observatory.DustCapKind, "east", observatory.TriggerDeactivation); n != 0 {
			t.Errorf("the deactivation started %d times, want none", n)
		}
		if slices.Contains(w.indi.changes(), "east-telescope Dust Cover Simulator.CAP_PARK PARK=On UNPARK=Off") {
			t.Errorf("changes = %q, want no park of the dust cap", w.indi.changes())
		}
	})
}
