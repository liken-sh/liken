package main

// A device that joins a server while the reservation is Ready gets the
// power of the output that its spec.power names, as PowerOn gives it at
// the start of the reservation. leavedeactivation_test.go holds the
// device that leaves and loses its power.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// outputOn answers the change that switches on one output of Switch
// east.
func outputOn(output string) string {
	return "east-telescope Simulator IO.DIGITAL_OUTPUT_" + output + " OFF=Off ON=On"
}

// The camera that leaves loses output 1, and gets it again when it
// joins. A flat panel created in the train during the session names
// output 4, which nothing switched on before. The Event on the device
// whose pod the runner creates says which output went on, and the
// output goes on before that pod exists, because a driver of real
// hardware connects only to a device with power.
func TestADeviceThatJoinsGetsItsPower(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		join   func(w *world)
		kind   observatory.Kind
		device string
		// indi names the INDI device that the joining device's driver
		// defines.
		indi  string
		sent  string
		event string
	}{
		{"a camera taken off the shelf",
			func(w *world) {
				w.moveDevice(observatory.CameraKind, "east-main", "", "")
				w.until(5*time.Minute, "the camera's output stays on", func() bool {
					return slices.Contains(w.indi.changes(), outputsOff[0])
				})
				w.moveDevice(observatory.CameraKind, "east-main", "opticalTrain", "east-imaging")
			},
			observatory.CameraKind, "east-main", "CCD Simulator", outputOn("1"),
			"Normal PodCreated: Created pod east-main-camera while Reservation east-tonight is Ready. Switched on output 1 of Switch east."},
		{"a flat panel created in the train",
			func(w *world) {
				w.put(observatory.FlatPanelKind, "extra", map[string]any{"opticalTrain": "east-imaging",
					"driver": map[string]any{"name": "indi_fake_flipflat", "image": variantImage},
					"power":  map[string]any{"switch": "east", "output": 4}})
			},
			observatory.FlatPanelKind, "extra", "Flip-Flat Simulator", outputOn("4"),
			"Normal PodCreated: Created pod extra-flatpanel while Reservation east-tonight is Ready. Switched on output 4 of Switch east."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := readyWorld(t)
				before := len(w.indi.changes())
				c.join(w)
				w.until(5*time.Minute, "the joining device does not connect", func() bool {
					return slices.Contains(w.indi.connected("east-telescope"), c.indi)
				})
				if !slices.Contains(w.indi.changes()[before:], c.sent) {
					t.Errorf("changes = %q\nwant %q", w.indi.changes()[before:], c.sent)
				}
				if got := typedEvents(w.api, c.kind, c.device); !slices.Contains(got, c.event) {
					t.Errorf("Events = %q\nwant %q", got, c.event)
				}
			})
		})
	}
}
