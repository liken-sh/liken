package main

// A Switch output that powers devices on more than one server. The
// release of one server leaves on each output that a device on another
// running server still names, because that device still draws from it.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// labOutput answers the change that sets one output of an observatory
// Switch lab to member.
func labOutput(output, member string) string {
	if member == "OFF" {
		return "lab-observatory Simulator IO.DIGITAL_OUTPUT_" + output + " OFF=On ON=Off"
	}
	return "lab-observatory Simulator IO.DIGITAL_OUTPUT_" + output + " OFF=Off ON=On"
}

// powerFrom sets the spec.power of a device and keeps the rest of its
// spec.
func (w *world) powerFrom(kind observatory.Kind, name, sw string, output int) {
	object, _ := w.api.object(kindCollection(kind), name)
	spec := object["spec"].(map[string]any)
	spec["power"] = map[string]any{"switch": sw, "output": output}
	w.put(kind, name, spec)
}

// The observatory's Switch lab powers the east focuser and the west
// camera from output 1, and the east rotator alone from output 2. The
// release of east switches off output 2, and leaves output 1 on for the
// west camera.
func TestAReleaseLeavesOnAnOutputThatAnotherServerStillUses(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.put(observatory.SwitchKind, "lab", map[string]any{"observatory": "lab", "driver": map[string]any{"name": "indi_simulator_io"}})
		w.powerFrom(observatory.FocuserKind, "east", "lab", 1)
		w.powerFrom(observatory.CameraKind, "west-main", "lab", 1)
		w.powerFrom(observatory.RotatorKind, "east", "lab", 2)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.reserve("west-tonight", map[string]any{"telescope": "west", "holder": "desktop"})
		w.phase("west-tonight", observatory.ReservationReady, 10*time.Minute)
		before := len(w.indi.changes())

		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.gone(observatory.ReservationKind, "east-tonight", 10*time.Minute)

		sent := w.indi.changes()[before:]
		if !slices.Contains(sent, labOutput("2", "OFF")) || slices.Contains(sent, labOutput("1", "OFF")) {
			t.Errorf("changes = %q, want output 2 off and output 1 left on", sent)
		}
	})
}

// Two releases that overlap switch off an output that only their own
// devices name. Each release finds the other server's pod while it
// switches its outputs off, so the output goes off only when a release
// counts no device whose server stops. The hold on the mounts' park
// keeps both releases in their Deactivation step until both have
// begun, and the holds on the servers' pods keep each one terminating
// until both have stopped.
func TestOverlappingReleasesSwitchOffASharedOutput(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.put(observatory.SwitchKind, "lab", map[string]any{"observatory": "lab", "driver": map[string]any{"name": "indi_simulator_io"}})
		w.powerFrom(observatory.FocuserKind, "east", "lab", 1)
		w.powerFrom(observatory.CameraKind, "west-main", "lab", 1)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.reserve("west-tonight", map[string]any{"telescope": "west", "holder": "desktop"})
		w.phase("west-tonight", observatory.ReservationReady, 10*time.Minute)
		before := len(w.indi.changes())
		servers := []string{"east-telescope", "west-telescope"}
		for _, name := range servers {
			w.holdPod(name)
		}

		w.indi.hold("Telescope Simulator", "TELESCOPE_PARK")
		for _, name := range []string{"east-tonight", "west-tonight"} {
			w.api.deleteNamed(kindCollection(observatory.ReservationKind), name)
			w.until(time.Minute, name+" does not park its mount", func() bool {
				r, _ := w.reservation(name)
				return strings.Contains(stepText(stepOf(r, observatory.StepDeactivation)), "Parked Running")
			})
		}
		w.indi.release("Telescope Simulator", "TELESCOPE_PARK")
		parked(w, "Telescope Simulator")
		w.until(10*time.Minute, "the servers' pods do not stop", func() bool {
			for _, name := range servers {
				object, _ := w.api.object(podsCollection, name)
				if object["metadata"].(map[string]any)["deletionTimestamp"] == nil {
					return false
				}
			}
			return true
		})
		for _, name := range servers {
			w.releasePod(name)
		}
		w.gone(observatory.ReservationKind, "east-tonight", 10*time.Minute)
		w.gone(observatory.ReservationKind, "west-tonight", 10*time.Minute)

		if sent := w.indi.changes()[before:]; !slices.Contains(sent, labOutput("1", "OFF")) {
			t.Errorf("changes = %q, want output 1 off", sent)
		}
	})
}
