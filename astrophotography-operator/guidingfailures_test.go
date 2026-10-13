package main

// What the runner does when guiding fails: it keeps the target, sets
// Imaging False with GuidingFailed once, and guides again a minute
// later, before the next frame. A star lost during an exposure does not
// stop the exposure, and the frame counts.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
	"github.com/liken-sh/liken/observatory-operator/phd2/phd2test"
)

// A failure to start guiding keeps the claim, posts one Warning however
// often it repeats, and ends when PHD2 guides again.
func TestAFailureToGuideRetriesBeforeTheNextFrame(t *testing.T) {
	cases := []struct {
		name        string
		break_, fix func(*phd2test.Server)
	}{
		{"the settle fails",
			func(s *phd2test.Server) { s.SettleFailure = "timed-out waiting for guider to settle" },
			func(s *phd2test.Server) { s.SettleFailure = "" }},
		{"no star",
			func(s *phd2test.Server) { s.Refusals = map[string]string{"find_star": "could not find star"} },
			func(s *phd2test.Server) { s.Refusals = nil }},
		{"no camera",
			func(s *phd2test.Server) { s.Equipment = false },
			func(s *phd2test.Server) { s.Equipment = true }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := guidedNight(t, target("m42", lights(1)))
				w.phd2.now().Set(c.break_)
				w.start(t)
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False GuidingFailed" })
				time.Sleep(5 * time.Minute)
				if got := w.targetNow(t, "m42").Status.Reservation; got != tonight {
					t.Errorf("the target is claimed by %q", got)
				}
				w.phd2.now().Set(c.fix)
				w.done(t, "m42")
				want := []string{"Slewing", "Guiding", "GuidingFailed", "Exposing", "SequencesDone"}
				if got := w.eventReasons("Target", "m42"); !slices.Equal(got, want) {
					t.Errorf("Events on the target %q, want %q", got, want)
				}
			})
		})
	}
}

// A dither that PHD2 refuses fails the guiding after the frame, which
// still counts, and the runner guides again before the next frame.
func TestARefusedDitherGuidesAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := guidedNight(t, dithered(1))
		w.phd2.now().Set(func(s *phd2test.Server) { s.Refusals = map[string]string{"dither": "Dither error"} })
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False GuidingFailed" })
		w.phd2.now().Set(func(s *phd2test.Server) { s.Refusals = nil })
		w.done(t, "m42")
		if got := pendingOf(w.targetNow(t, "m42").Status); !slices.Equal(got, []int32{5}) {
			t.Errorf("pending %v, want [5]", got)
		}
		if got := guidingCalls(w.phd2.now()); got != "loop find_star guide dither loop find_star guide dither dither dither" {
			t.Errorf("PHD2 received %q", got)
		}
		requireSample(t, scrape(t, w.o.metrics), `astrophotography_guiding_failures_total{namespace="observatory",target="m42"} 1`)
	})
}

// A star lost during an exposure does not stop it. The frame counts,
// because grading decides later whether it is good (plan 06), and the
// runner finds a star and guides again before the next frame.
func TestALostStarDuringAnExposureGuidesAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := guidedNight(t, target("m42", lights(2)))
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		w.phd2.now().Broadcast(phd2test.GuideStep(1, 0.2, 0.1))
		w.phd2.now().Broadcast(phd2test.Event("StarLost", map[string]any{"Frame": 2, "Status": -2}))
		w.done(t, "m42")
		if got := pendingOf(w.targetNow(t, "m42").Status); !slices.Equal(got, []int32{2}) {
			t.Errorf("pending %v, want [2]", got)
		}
		if got := guidingCalls(w.phd2.now()); got != "loop find_star guide loop find_star guide" {
			t.Errorf("PHD2 received %q", got)
		}
	})
}

// A PHD2 that exits during an exposure takes no dither after the frame,
// which counts. The next frame waits until the guider's new PHD2
// guides.
func TestAPHD2ThatExitsDuringAnExposure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := guidedNight(t, dithered(1))
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		first := w.phd2.now()
		first.Cut()
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False GuidingFailed" })
		if got := pendingOf(w.targetNow(t, "m42").Status); !slices.Equal(got, []int32{1}) {
			t.Errorf("pending %v, want [1]", got)
		}
		second := connectedPHD2()
		w.phd2.replace(second)
		w.done(t, "m42")
		if got := guidingCalls(first); got != "loop find_star guide" {
			t.Errorf("the first PHD2 received %q", got)
		}
		if got := guidingCalls(second); got != "loop find_star guide dither dither dither" {
			t.Errorf("the new PHD2 received %q", got)
		}
	})
}

// A Guider with no endpoint yet, as while its pod starts, fails the
// guiding until PHD2 answers, and the runner keeps the target.
func TestAGuiderWithNoEndpoint(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(guidersPath, guider(nil))
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, guided(readyOrNot(observatory.ReservationReady)))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False GuidingFailed" })
		if len(w.phd2.addresses()) != 0 {
			t.Errorf("dialed %q", w.phd2.addresses())
		}
		w.put(guidersPath, guider(guiderEndpoint))
		w.done(t, "m42")
		want := []string{"Slewing", "GuidingFailed", "Exposing", "SequencesDone"}
		if got := w.eventReasons("Target", "m42"); !slices.Equal(got, want) {
			t.Errorf("Events on the target %q, want %q", got, want)
		}
	})
}

// A reservation that ends while PHD2 settles stops the runner, which
// takes no frame and reports no failure: the supervisor releases the
// target with Waiting.
func TestAReservationThatEndsWhilePHD2Settles(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := guidedNight(t, target("m42", lights(1)))
		w.phd2.now().Set(func(s *phd2test.Server) { s.HoldSettle = true })
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "True Guiding" })
		w.put(reservationsPath, guided(readyOrNot(observatory.ReservationDeactivating)))
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False Waiting" })
		if frames := w.worker.held(); len(frames) != 0 {
			t.Errorf("frames %q, want none", frames)
		}
		want := []string{"Slewing", "Guiding", "Waiting"}
		if got := w.eventReasons("Target", "m42"); !slices.Equal(got, want) {
			t.Errorf("Events on the target %q, want %q", got, want)
		}
	})
}
