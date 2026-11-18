package main

// When guiding fails, the runner keeps the Target, sets Imaging to
// False with the reason GuidingFailed, and guides again after
// failedGuidingWait, before the next frame.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
	"github.com/liken-sh/liken/observatory-operator/phd2/phd2test"
)

// The runner posts one GuidingFailed Event however often the failure
// repeats.
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
				w.phd2.current().Set(c.break_)
				w.start(t)
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False GuidingFailed" })
				time.Sleep(5 * time.Minute)
				if got := w.targetNow(t, "m42").Status.Reservation; got != tonight {
					t.Errorf("after 5 minutes the target is claimed by %q, want %q", got, tonight)
				}
				w.phd2.current().Set(c.fix)
				w.done(t, "m42")
				want := []string{"Slewing", "Guiding", "GuidingFailed", "Exposing", "SequencesDone"}
				if got := w.eventReasons("Target", "m42"); !slices.Equal(got, want) {
					t.Errorf("the Events on the target have the reasons %q, want %q", got, want)
				}
			})
		})
	}
}

// A refused dither fails guiding after the frame, and the frame still
// counts.
func TestARefusedDitherGuidesAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := guidedNight(t, dithered(1))
		w.phd2.current().Set(func(s *phd2test.Server) { s.Refusals = map[string]string{"dither": "Dither error"} })
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False GuidingFailed" })
		w.phd2.current().Set(func(s *phd2test.Server) { s.Refusals = nil })
		w.done(t, "m42")
		if got := pendingOf(w.targetNow(t, "m42").Status); !slices.Equal(got, []int32{5}) {
			t.Errorf("the pending frames are %v, want [5]", got)
		}
		if got := guidingCalls(w.phd2.current()); got != "loop find_star guide dither loop find_star guide dither dither dither" {
			t.Errorf("PHD2 received %q, want %q", got, "loop find_star guide dither loop find_star guide dither dither dither")
		}
		requireSample(t, scrape(t, w.o.metrics), `astrophotography_guiding_failures_total{namespace="observatory",target="m42"} 1`)
	})
}

// A star lost during an exposure does not stop it, and the frame
// counts, because the runner does not judge a frame by its guiding.
func TestALostStarDuringAnExposureGuidesAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := guidedNight(t, target("m42", lights(2)))
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		w.phd2.current().Broadcast(phd2test.GuideStep(1, 0.2, 0.1))
		w.phd2.current().Broadcast(phd2test.Event("StarLost", map[string]any{"Frame": 2, "Status": -2}))
		w.done(t, "m42")
		if got := pendingOf(w.targetNow(t, "m42").Status); !slices.Equal(got, []int32{2}) {
			t.Errorf("the pending frames are %v, want [2]", got)
		}
		if got := guidingCalls(w.phd2.current()); got != "loop find_star guide loop find_star guide" {
			t.Errorf("PHD2 received %q, want %q", got, "loop find_star guide loop find_star guide")
		}
	})
}

// The runner does not dither after the frame, and the next frame waits
// until the Guider's new PHD2 is guiding.
func TestAPHD2ThatExitsDuringAnExposureStillCountsTheFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := guidedNight(t, dithered(1))
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		first := w.phd2.current()
		first.Cut()
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False GuidingFailed" })
		if got := pendingOf(w.targetNow(t, "m42").Status); !slices.Equal(got, []int32{1}) {
			t.Errorf("the pending frames are %v, want [1]", got)
		}
		second := connectedPHD2()
		w.phd2.replace(second)
		w.done(t, "m42")
		if got := guidingCalls(first); got != "loop find_star guide" {
			t.Errorf("the first PHD2 received %q, want %q", got, "loop find_star guide")
		}
		if got := guidingCalls(second); got != "loop find_star guide dither dither dither" {
			t.Errorf("the new PHD2 received %q, want %q", got, "loop find_star guide dither dither dither")
		}
	})
}

// A Guider has no endpoint while its pod starts.
func TestAGuiderWithNoEndpointFailsGuidingUntilItHasOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(guidersPath, guider(nil))
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, guided(reservationIn(observatory.ReservationReady)))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False GuidingFailed" })
		if len(w.phd2.addresses()) != 0 {
			t.Errorf("the operator dialed %q, want no address because the Guider has no endpoint", w.phd2.addresses())
		}
		w.put(guidersPath, guider(guiderEndpoint))
		w.done(t, "m42")
		want := []string{"Slewing", "GuidingFailed", "Exposing", "SequencesDone"}
		if got := w.eventReasons("Target", "m42"); !slices.Equal(got, want) {
			t.Errorf("the Events on the target have the reasons %q, want %q", got, want)
		}
	})
}

// The runner reports no failure, and the supervisor in runners.go
// releases the Target with the reason Waiting.
func TestAReservationThatEndsWhilePHD2SettlesTakesNoFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := guidedNight(t, target("m42", lights(1)))
		w.phd2.current().Set(func(s *phd2test.Server) { s.HoldSettle = true })
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "True Guiding" })
		w.put(reservationsPath, guided(reservationIn(observatory.ReservationDeactivating)))
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False Waiting" })
		if frames := w.worker.outboxFrames(); len(frames) != 0 {
			t.Errorf("the worker holds the frames %q, want none", frames)
		}
		want := []string{"Slewing", "Guiding", "Waiting"}
		if got := w.eventReasons("Target", "m42"); !slices.Equal(got, want) {
			t.Errorf("the Events on the target have the reasons %q, want %q", got, want)
		}
	})
}
