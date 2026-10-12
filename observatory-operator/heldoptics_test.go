package main

// What a session records about a telescope's optics and its Guider, so
// an edit during the session changes nothing that the session started:
// the SCOPE_INFO that a camera's driver gets when it comes back, and
// the PHD2 that guides.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// An edit of an OpticalTube, an OpticalTrain, or a Guider during
// east-tonight takes effect when the session ends. The main camera,
// whose pod an eviction deletes after the edit, gets the SCOPE_INFO of
// the tube that the session started with. PHD2 keeps its pod, so it
// keeps its calibration, and the Guider stays Ready. A Guider created
// during the session starts no PHD2.
func TestAnEditOfTheOpticsOrTheGuiderDuringASessionChangesNothingItStarted(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(w *world)
	}{
		{"the imaging tube's optics", func(w *world) {
			w.amend(observatory.OpticalTubeKind, "east-refractor", func(spec map[string]any) { spec["focalLength"], spec["aperture"] = 960, 100 })
		}},
		{"the imaging train's tube", func(w *world) {
			w.amend(observatory.OpticalTrainKind, "east-imaging", func(spec map[string]any) { spec["opticalTube"] = "east-guidescope" })
		}},
		{"the guide tube's focal length", func(w *world) {
			w.amend(observatory.OpticalTubeKind, "east-guidescope", func(spec map[string]any) { spec["focalLength"] = 400 })
		}},
		{"the guide train moved to another telescope", func(w *world) {
			w.amend(observatory.OpticalTrainKind, "east-guiding", func(spec map[string]any) { spec["telescope"] = "west" })
		}},
		{"the Guider's pulses", func(w *world) {
			w.amend(observatory.GuiderKind, "east", func(spec map[string]any) { spec["pulses"] = "Camera" })
		}},
		{"the Guider's train", func(w *world) {
			w.amend(observatory.GuiderKind, "east", func(spec map[string]any) { spec["opticalTrain"] = "east-imaging" })
		}},
		{"the Guider moved to another telescope", func(w *world) {
			w.amend(observatory.GuiderKind, "east", func(spec map[string]any) { spec["telescope"] = "west" })
		}},
		{"a Guider created", func(w *world) {
			w.put(observatory.GuiderKind, "a-east", map[string]any{"telescope": "east", "opticalTrain": "east-imaging", "pulses": "Mount"})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := readyWorld(t)
				w.until(time.Minute, "the Guider is not Ready", func() bool { return guider(w).Status.Phase == observatory.PhaseReady })
				before, _ := w.api.object(podsCollection, "east-guider")
				from := len(w.indi.changes())

				c.edit(w)
				w.settle()
				w.api.deleteNamed(podsCollection, "east-main-camera")
				time.Sleep(5 * time.Minute)
				synctest.Wait()

				info := slices.DeleteFunc(touches(w, from, "CCD Simulator"), func(c string) bool { return !strings.Contains(c, ".SCOPE_INFO ") })
				if want := "east-telescope CCD Simulator.SCOPE_INFO FOCAL_LENGTH=480 APERTURE=80"; len(info) == 0 || slices.ContainsFunc(info, func(c string) bool { return c != want }) {
					t.Errorf("SCOPE_INFO = %q, want only %q", info, want)
				}
				after, ok := w.api.object(podsCollection, "east-guider")
				if !ok || podUID(after) != podUID(before) {
					t.Errorf("PHD2's pod was replaced or deleted")
				}
				if phase := guider(w).Status.Phase; phase != observatory.PhaseReady {
					t.Errorf("the Guider is %s, want Ready", phase)
				}
				if slices.Contains(w.api.names(podsCollection), "a-east-guider") {
					t.Errorf("pods = %q, want no PHD2 of the Guider created during the session", w.api.names(podsCollection))
				}
			})
		})
	}
}
