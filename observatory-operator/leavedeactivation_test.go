package main

// A device that leaves a telescope or an observatory that is Active,
// while its object still exists, runs its deactivation before its
// driver stops. devicedelete_test.go holds a device that a person
// deletes.

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
	moved := func(kind observatory.Kind, name, field, parent string) func(*world) {
		return func(w *world) { w.moveDevice(kind, name, field, parent) }
	}
	cases := []struct {
		name        string
		kind        observatory.Kind
		device, pod string
		leave       func(w *world)
		// parkMount parks the mount first, because the dome's lock
		// refuses a park while a mount is unparked.
		parkMount bool
		sent      string
	}{
		{"a dust cap put on the shelf", observatory.DustCapKind, "east", "east-dustcap", moved(observatory.DustCapKind, "east", "", ""), false,
			"east-telescope Dust Cover Simulator.CAP_PARK PARK=On UNPARK=Off"},
		{"a dust cap moved to another telescope", observatory.DustCapKind, "east", "east-dustcap", moved(observatory.DustCapKind, "east", "opticalTrain", "west-imaging"), false,
			"east-telescope Dust Cover Simulator.CAP_PARK PARK=On UNPARK=Off"},
		{"a dust cap whose train is deleted", observatory.DustCapKind, "east", "east-dustcap",
			func(w *world) { w.api.deleteNamed(kindCollection(observatory.OpticalTrainKind), "east-imaging") }, false,
			"east-telescope Dust Cover Simulator.CAP_PARK PARK=On UNPARK=Off"},
		{"a dome put on the shelf", observatory.DomeKind, "lab", "lab-dome", moved(observatory.DomeKind, "lab", "", ""), true,
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
				c.leave(w)
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

// A device that leaves and joins again in one session runs its
// procedures each time: a cap that its deactivation closed opens again
// when it rejoins, and closes again when it leaves again. The
// reservation's Activation step ran the first activation. The fake
// driver, like the simulator, starts open in each new pod, so the test
// counts the runs rather than the moves.
func TestADeviceThatLeavesAndRejoinsRunsEachProcedureAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		for round := 1; round <= 2; round++ {
			w.moveDevice(observatory.DustCapKind, "east", "", "")
			w.until(5*time.Minute, "the dust cap's pod stays", func() bool {
				return !slices.Contains(w.api.names(podsCollection), "east-dustcap")
			})
			w.moveDevice(observatory.DustCapKind, "east", "opticalTrain", "east-imaging")
			w.until(5*time.Minute, "the rejoined dust cap does not activate", func() bool {
				return startedRuns(w.api, observatory.DustCapKind, "east", observatory.TriggerActivation) == round+1 &&
					lastRun(t, w, observatory.DustCapKind, "east", observatory.TriggerActivation).State == observatory.StepDone
			})
		}
		if n := startedRuns(w.api, observatory.DustCapKind, "east", observatory.TriggerDeactivation); n != 2 {
			t.Errorf("the deactivation started %d times, want 2", n)
		}
	})
}

// A device that leaves while the reservation is Ready loses the power
// of the output that its spec.power names, as it would at the end of
// the reservation: its Switch stays on the server, and a camera left
// powered on the shelf would stay powered for good. A train deleted
// before its reservation, which the operator can read first in a
// `kubectl delete -f`, takes every device of the train off the server.
func TestADeviceThatLeavesLosesItsPower(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		leave func(w *world)
		pods  []string
		off   []string
	}{
		{"a camera put on the shelf",
			func(w *world) { w.moveDevice(observatory.CameraKind, "east-main", "", "") },
			[]string{"east-main-camera"}, outputsOff[:1]},
		{"a camera moved to another telescope",
			func(w *world) { w.moveDevice(observatory.CameraKind, "east-main", "opticalTrain", "west-imaging") },
			[]string{"east-main-camera"}, outputsOff[:1]},
		{"a train deleted before its reservation",
			func(w *world) { w.api.deleteNamed(kindCollection(observatory.OpticalTrainKind), "east-imaging") },
			[]string{"east-main-camera", "east-focuser", "east-rotator", "east-dustcap", "east-flatpanel"}, outputsOff},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := readyWorld(t)
				c.leave(w)
				w.until(5*time.Minute, "a pod that left stays", func() bool {
					names := w.api.names(podsCollection)
					return !slices.ContainsFunc(c.pods, func(p string) bool { return slices.Contains(names, p) })
				})
				w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
				w.gone(observatory.ReservationKind, "east-tonight", 10*time.Minute)
				for _, change := range c.off {
					if !slices.Contains(w.indi.changes(), change) {
						t.Errorf("changes = %q\nwant %q", w.indi.changes(), change)
					}
				}
			})
		})
	}
}

// The Event on a device that left says that its output went off, and
// an output that a device on the server still names stays on: the
// focuser and the rotator share output 2.
func TestALeavingDeviceSwitchesOffOnlyAnOutputNoDeviceStillNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		kind   observatory.Kind
		device string
		event  string
		output string
		sent   bool
	}{
		{"a camera alone on output 1", observatory.CameraKind, "east-main",
			"Normal PodDeleted: Deleted pod east-main-camera, because the Camera left Telescope east while Reservation east-tonight is Ready. Switched off output 1 of Switch east.",
			outputsOff[0], true},
		{"a focuser that shares output 2", observatory.FocuserKind, "east",
			"Normal PodDeleted: Deleted pod east-focuser, because the Focuser left Telescope east while Reservation east-tonight is Ready.",
			outputsOff[1], false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := readyWorld(t)
				w.moveDevice(c.kind, c.device, "", "")
				w.until(5*time.Minute, "no PodDeleted Event", func() bool {
					return slices.Contains(typedEvents(w.api, c.kind, c.device), c.event)
				})
				if sent := slices.Contains(w.indi.changes(), c.output); sent != c.sent {
					t.Errorf("sent %q = %t, want %t", c.output, sent, c.sent)
				}
			})
		})
	}
}
