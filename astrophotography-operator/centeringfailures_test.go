package main

// What ends a centering short: a frame that does not solve at twice
// its exposure, rounds that run out, a plate scale the runner cannot
// compute, a worker that fails, and a Solver that is not Ready, which
// images the target uncentered.

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/solver"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// exposures answers the length of each exposure the CCD started, in
// seconds, in order.
func exposures(w *night) []string {
	var out []string
	for _, change := range w.indi.Changes() {
		if strings.HasPrefix(change, ccdINDI+".CCD_EXPOSURE ") {
			out = append(out, member([]string{change}, ccdINDI+".CCD_EXPOSURE", "CCD_EXPOSURE_VALUE"))
		}
	}
	return out
}

// imagingMessage answers the message of a target's Imaging condition.
func (w *night) imagingMessage(t *testing.T, name string) string {
	imaging, _ := conditions.Find(w.targetNow(t, name).Status.Conditions, astrophotography.ConditionImaging)
	return imaging.Message
}

// Each failure sets Imaging False with CenteringFailed and the reason
// in its message, and releases the target. A centering filter that the
// wheel does not hold fails with NoFilter, as a sequence's does.
func TestCenteringFails(t *testing.T) {
	cases := []struct {
		name    string
		change  func(*night, *observatory.Reservation)
		message string
		reason  string
	}{
		{"no frame solves", func(w *night, _ *observatory.Reservation) {
			w.worker.peers.set(func(f *fakePeers) { f.reach = -1 })
		}, "no centering frame of round 1 solved, at 5s or at 10s", ""},
		{"the rounds run out", func(w *night, _ *observatory.Reservation) {
			w.worker.peers.set(func(f *fakePeers) {
				f.answers = []solver.SolveAnswer{northOf(m42Position(), 600), northOf(m42Position(), 600), northOf(m42Position(), 600), northOf(m42Position(), 600), northOf(m42Position(), 600)}
			})
		}, "after 5 rounds the error is 600.0 arcseconds, more than the tolerance of 30", ""},
		{"the worker fails", func(w *night, _ *observatory.Reservation) {
			w.worker.peers.set(func(f *fakePeers) { f.failExtract = true })
		}, "cannot open the frame", ""},
		{"the camera reports no pixel size", func(w *night, _ *observatory.Reservation) {
			w.indi.Report(ccdINDI, "CCD_INFO", "CCD_PIXEL_SIZE", "0")
		}, "CCD Simulator reports no pixel size in CCD_INFO", ""},
		{"the session has no tube for the train", func(_ *night, r *observatory.Reservation) {
			r.Status.Session.Tubes = nil
		}, `the session has no OpticalTube of train "east-imaging"`, ""},
		{"the camera refuses the exposure", func(w *night, _ *observatory.Reservation) {
			w.indi.Refuse(ccdINDI, "CCD_EXPOSURE")
		}, "CCD_EXPOSURE", ""},
		{"the mount refuses the sync", func(w *night, _ *observatory.Reservation) {
			w.worker.peers.set(func(f *fakePeers) { f.answers = []solver.SolveAnswer{northOf(m42Position(), 600)} })
			w.indi.Refuse(mountINDI, "ON_COORD_SET")
		}, "syncing the mount", ""},
		{"the centering exposure does not read", func(w *night, _ *observatory.Reservation) {
			c := centeringOn("lab")
			c.Exposure = "soon"
			w.put(targetsPath, centeringTarget(c))
		}, `spec.centering.exposure "soon"`, ""},
		{"the wheel has no centering filter", func(w *night, _ *observatory.Reservation) {
			c := centeringOn("lab")
			c.Filter = "Methane"
			w.put(targetsPath, centeringTarget(c))
		}, `no filter named "Methane"`, astrophotography.ReasonNoFilter},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := centeringWorld(t, centeringOn("lab"))
				res := readyOrNot(observatory.ReservationReady)
				c.change(w, res)
				w.put(reservationsPath, res)
				reason := cmp.Or(c.reason, astrophotography.ReasonCenteringFailed)
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False "+reason })
				if got := w.imagingMessage(t, "m42"); !strings.Contains(got, c.message) {
					t.Errorf("Imaging says %q, want %q in it", got, c.message)
				}
				if got := w.targetNow(t, "m42").Status.Reservation; got != "" {
					t.Errorf("the target is still claimed by %q", got)
				}
			})
		})
	}
}

// A frame that does not solve is taken once more at twice the
// exposure, and both frames leave the outbox.
func TestAFrameThatDoesNotSolveIsTakenAgainLonger(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := centeringWorld(t, centeringOn("lab"))
		w.worker.peers.set(func(f *fakePeers) { f.reach = -1 })
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False CenteringFailed" })
		if got := exposures(w); !slices.Equal(got, []string{"5", "10"}) {
			t.Errorf("exposures %q, want 5 and 10", got)
		}
		if got := w.worker.deletions(); len(got) != 2 {
			t.Errorf("deleted %q, want both centering frames", got)
		}
		got := w.targetNow(t, "m42").Status.Centering
		if got == nil || got.Rounds != 1 || got.Solution != nil || got.Error != nil {
			t.Errorf("status.centering %+v, want one round and no solution", got)
		}
	})
}

// After the last round outside the tolerance, the runner gives up with
// no more sync, and records the last error.
func TestCenteringGivesUpAfterItsRounds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := centeringOn("lab")
		c.Attempts = 3
		w := centeringNight(t, c, northOf(m42Position(), 600), northOf(m42Position(), 300), northOf(m42Position(), 120))
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False CenteringFailed" })
		got := w.targetNow(t, "m42").Status.Centering
		if got == nil || got.Rounds != 3 || got.Error == nil || math.Abs(*got.Error-120) > 0.1 {
			t.Errorf("status.centering %+v, want 3 rounds and an error of 120", got)
		}
		if n := syncs(w); n != 2 {
			t.Errorf("%d syncs, want 2", n)
		}
		requireSample(t, scrape(t, w.o.metrics), "astrophotography_centering_rounds_sum 3")
	})
}

// A target whose Solver is missing or not Ready is imaged where the
// slew left the mount. The Imaging condition says why, the reservation
// gets a Warning, and the runner takes no centering frame.
func TestATargetWhoseSolverIsNotReadyIsImagedUncentered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := centeringNight(t, centeringOn("other"))
		waitUntil(t, w.inFlight(t, "m42"))
		want := "Reservation east-tonight exposes Luminance 10s, unguided because the telescope has no Guider, not centered because Solver other is not Ready."
		if got := w.imagingMessage(t, "m42"); got != want {
			t.Errorf("Imaging says %q, want %q", got, want)
		}
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		if got := w.eventReasons("Reservation", tonight); !slices.Contains(got, reasonNotCentered) {
			t.Errorf("Events on the reservation %q, want %s", got, reasonNotCentered)
		}
		if got := exposures(w); len(got) != 1 {
			t.Errorf("exposures %q, want the one light frame", got)
		}
		if got := w.targetNow(t, "m42").Status.Centering; got != nil {
			t.Errorf("status.centering %+v, want none", got)
		}
	})
}

// A Solver that goes away between two rounds ends the centering, and
// the runner images the target where the last slew left the mount.
func TestASolverThatGoesAwayEndsTheCentering(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := centeringWorld(t, centeringOn("lab"), northOf(m42Position(), 1200))
		w.worker.peers.set(func(f *fakePeers) {
			f.solved = func() { w.api.remove(solversPath, ns, "lab") }
		})
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		waitUntil(t, w.inFlight(t, "m42"))
		if got := w.imagingMessage(t, "m42"); !strings.HasSuffix(got, "not centered because Solver lab is not Ready.") {
			t.Errorf("Imaging says %q", got)
		}
		if n := syncs(w); n != 1 {
			t.Errorf("%d syncs, want the one of the round that solved", n)
		}
	})
}
