package main

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

// exposures returns the length in seconds of each exposure the CCD
// started, in order.
func exposures(w *runnerWorld) []string {
	var out []string
	for _, change := range w.indi.Changes() {
		if strings.HasPrefix(change, ccdINDI+".CCD_EXPOSURE ") {
			out = append(out, member([]string{change}, ccdINDI+".CCD_EXPOSURE", "CCD_EXPOSURE_VALUE"))
		}
	}
	return out
}

func (w *runnerWorld) imagingMessage(t *testing.T, name string) string {
	imaging, _ := conditions.Find(w.targetNow(t, name).Status.Conditions, astrophotography.ConditionImaging)
	return imaging.Message
}

// Each failure sets Imaging to False with reason CenteringFailed, puts
// the cause in the message, and releases the Target. A centering filter
// that the wheel does not have fails with NoFilter, the same as a
// sequence's filter does.
func TestCenteringFails(t *testing.T) {
	cases := []struct {
		name    string
		change  func(*runnerWorld, *observatory.Reservation)
		message string
		reason  string
	}{
		{"no frame solves", func(w *runnerWorld, _ *observatory.Reservation) {
			w.worker.peers.set(func(f *fakePeers) { f.reach = -1 })
		}, "no centering frame of round 1 solved, at 5s or at 10s", ""},
		{"the rounds run out", func(w *runnerWorld, _ *observatory.Reservation) {
			w.worker.peers.set(func(f *fakePeers) {
				f.solutions = []solver.SolveResponse{northOf(m42Position(), 600), northOf(m42Position(), 600), northOf(m42Position(), 600), northOf(m42Position(), 600), northOf(m42Position(), 600)}
			})
		}, "after 5 rounds the error is 600.0 arcseconds, more than the tolerance of 30", ""},
		{"the worker fails", func(w *runnerWorld, _ *observatory.Reservation) {
			w.worker.peers.set(func(f *fakePeers) { f.failExtract = true })
		}, "cannot open the frame", ""},
		{"the camera reports no pixel size", func(w *runnerWorld, _ *observatory.Reservation) {
			w.indi.Report(ccdINDI, "CCD_INFO", "CCD_PIXEL_SIZE", "0")
		}, "CCD Simulator reports no pixel size in CCD_INFO", ""},
		{"the session has no tube for the train", func(_ *runnerWorld, r *observatory.Reservation) {
			r.Status.Session.Tubes = nil
		}, `the session has no OpticalTube of train "east-imaging"`, ""},
		{"the camera refuses the exposure", func(w *runnerWorld, _ *observatory.Reservation) {
			w.indi.Refuse(ccdINDI, "CCD_EXPOSURE")
		}, "CCD_EXPOSURE", ""},
		{"the mount refuses the sync", func(w *runnerWorld, _ *observatory.Reservation) {
			w.worker.peers.set(func(f *fakePeers) { f.solutions = []solver.SolveResponse{northOf(m42Position(), 600)} })
			w.indi.Refuse(mountINDI, "ON_COORD_SET")
		}, "syncing the mount", ""},
		{"the centering exposure does not read", func(w *runnerWorld, _ *observatory.Reservation) {
			c := centeringOn("lab")
			c.Exposure = "soon"
			w.put(targetsPath, centeringTarget(c))
		}, `spec.centering.exposure "soon"`, ""},
		{"the wheel has no centering filter", func(w *runnerWorld, _ *observatory.Reservation) {
			c := centeringOn("lab")
			c.Filter = "Methane"
			w.put(targetsPath, centeringTarget(c))
		}, `no filter named "Methane"`, astrophotography.ReasonNoFilter},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := centeringWorld(t, centeringOn("lab"))
				res := reservationIn(observatory.ReservationReady)
				c.change(w, res)
				w.put(reservationsPath, res)
				reason := cmp.Or(c.reason, astrophotography.ReasonCenteringFailed)
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False "+reason })
				if got := w.imagingMessage(t, "m42"); !strings.Contains(got, c.message) {
					t.Errorf("the Imaging message is %q, want it to contain %q", got, c.message)
				}
				if got := w.targetNow(t, "m42").Status.Reservation; got != "" {
					t.Errorf("status.reservation is %q, want it cleared", got)
				}
			})
		})
	}
}

// A frame that does not solve is taken again at twice the exposure,
// and both frames are deleted from the outbox.
func TestAFrameThatDoesNotSolveIsTakenAgainLonger(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := centeringWorld(t, centeringOn("lab"))
		w.worker.peers.set(func(f *fakePeers) { f.reach = -1 })
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False CenteringFailed" })
		if got := exposures(w); !slices.Equal(got, []string{"5", "10"}) {
			t.Errorf("the exposures are %q, want 5 and 10", got)
		}
		if got := w.worker.deletions(); len(got) != 2 {
			t.Errorf("the deleted frames are %q, want both centering frames", got)
		}
		got := w.targetNow(t, "m42").Status.Centering
		if got == nil || got.Rounds != 1 || got.Solution != nil || got.Error != nil {
			t.Errorf("status.centering %+v, want one round and no solution", got)
		}
	})
}

// When the last round is still outside the tolerance, the runner gives
// up without another sync and records the last error.
func TestCenteringGivesUpAfterItsRounds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := centeringOn("lab")
		c.Attempts = 3
		w := centeringNight(t, c, northOf(m42Position(), 600), northOf(m42Position(), 300), northOf(m42Position(), 120))
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False CenteringFailed" })
		got := w.targetNow(t, "m42").Status.Centering
		if got == nil || got.Rounds != 3 || got.Error == nil || math.Abs(*got.Error-120) > 0.1 || !near(got.RoundErrors, []float64{600, 300, 120}) {
			t.Errorf("status.centering %+v, want 3 rounds and the errors 600, 300, and 120", got)
		}
		if n := syncs(w); n != 2 {
			t.Errorf("the mount received %d syncs, want 2", n)
		}
		requireSample(t, scrape(t, w.o.metrics), "astrophotography_centering_rounds_sum 3")
	})
}

// A centering that stops after at least one solved round records those
// rounds, whatever stopped it: a mount that refuses the sync, or a
// Solver that is deleted before the next round.
func TestACenteringCutShortRecordsItsRounds(t *testing.T) {
	cases := []struct {
		name   string
		change func(*runnerWorld)
		end    string
	}{
		{"the mount refuses the sync", func(w *runnerWorld) { w.indi.Refuse(mountINDI, "ON_COORD_SET") }, "False " + astrophotography.ReasonCenteringFailed},
		{"the Solver is deleted", func(w *runnerWorld) {
			w.worker.peers.set(func(f *fakePeers) { f.solved = func() { w.api.remove(solversPath, ns, "lab") } })
		}, "False SequencesDone"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := centeringWorld(t, centeringOn("lab"), northOf(m42Position(), 600))
				c.change(w)
				w.put(reservationsPath, reservationIn(observatory.ReservationReady))
				waitUntil(t, func() bool { return w.imaging(t, "m42") == c.end })
				got := w.targetNow(t, "m42").Status.Centering
				if got == nil || got.Rounds != 1 || !near(got.RoundErrors, []float64{600}) {
					t.Errorf("status.centering %+v, want one round with an error of 600", got)
				}
				requireSample(t, scrape(t, w.o.metrics), "astrophotography_centering_rounds_count 1")
			})
		})
	}
}

// A Target whose Solver is missing or not Ready is imaged where the
// slew left the mount. The Imaging message gives the reason, the
// Reservation gets a Warning Event, and the runner takes no centering
// frame.
func TestATargetWhoseSolverIsNotReadyIsImagedUncentered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := centeringNight(t, centeringOn("other"))
		waitUntil(t, w.inFlight(t, "m42"))
		want := "Reservation east-tonight exposes Luminance 10s, unguided because the telescope has no Guider, not centered because Solver other is not Ready"
		if got := w.imagingMessage(t, "m42"); got != want {
			t.Errorf("the Imaging message is %q, want %q", got, want)
		}
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		if got := w.eventReasons("Reservation", tonight); !slices.Contains(got, reasonNotCentered) {
			t.Errorf("the Events on the Reservation are %q, want %s among them", got, reasonNotCentered)
		}
		if got := exposures(w); len(got) != 1 {
			t.Errorf("the exposures are %q, want the one light frame", got)
		}
		if got := w.targetNow(t, "m42").Status.Centering; got != nil {
			t.Errorf("status.centering %+v, want none", got)
		}
	})
}

// A Solver that is deleted between two rounds ends the centering, and
// the runner images the Target at the mount's current position.
func TestASolverDeletedBetweenRoundsEndsTheCentering(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := centeringWorld(t, centeringOn("lab"), northOf(m42Position(), 1200))
		w.worker.peers.set(func(f *fakePeers) {
			f.solved = func() { w.api.remove(solversPath, ns, "lab") }
		})
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		waitUntil(t, w.inFlight(t, "m42"))
		if got := w.imagingMessage(t, "m42"); !strings.HasSuffix(got, "not centered because Solver lab is not Ready") {
			t.Errorf("the Imaging message is %q, want it to end with \"not centered because Solver lab is not Ready\"", got)
		}
		if n := syncs(w); n != 1 {
			t.Errorf("the mount received %d syncs, want the one of the round that solved", n)
		}
	})
}
