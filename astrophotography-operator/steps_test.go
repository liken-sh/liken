package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// steps implements guiding and centering and records each call. Calls
// of the step named in fails return an error, after the first passes
// calls of it succeed.
type steps struct {
	mu     sync.Mutex
	calls  []string
	fails  string
	passes int
}

func (s *steps) call(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, name)
	if name != s.fails {
		return nil
	}
	if s.passes > 0 {
		s.passes--
		return nil
	}
	return errors.New(name + " failed")
}

func (s *steps) center(context.Context, *work) (string, error) {
	return "centered by the test", s.call("center")
}
func (s *steps) open(*runner) func(context.Context) { return func(context.Context) {} }
func (s *steps) stop(context.Context, *work) error  { return s.call("stop") }
func (s *steps) start(context.Context, *work) (string, error) {
	return "guided by the test", s.call("start")
}
func (s *steps) afterFrame(_ context.Context, _ *work, frames int) error {
	return s.call(fmt.Sprintf("afterFrame %d", frames))
}

func (s *steps) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

// afterFrame is where dithering happens. The last frame gets no
// afterFrame.
func TestTheRunnerCallsGuidingAndCenteringInOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		recorded := &steps{}
		w.configure = func(o *operator) { o.centering, o.guiding = recorded, recorded }
		w.put(targetsPath, target("m42", lights(2)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		imaging, _ := conditions.Find(w.targetNow(t, "m42").Status.Conditions, astrophotography.ConditionImaging)
		if !strings.Contains(imaging.Message, "guided by the test, centered by the test") {
			t.Errorf("the Imaging message is %q, want it to say \"guided by the test, centered by the test\"", imaging.Message)
		}
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		want := []string{"stop", "center", "start", "afterFrame 1", "start"}
		if got := recorded.recorded(); !slices.Equal(got, want) {
			t.Errorf("calls %q, want %q", got, want)
		}
	})
}

func TestTheImagingMessageReportsAnUnguidedUncenteredTarget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		imaging, _ := conditions.Find(w.targetNow(t, "m42").Status.Conditions, astrophotography.ConditionImaging)
		if want := "Reservation east-tonight exposes Luminance 10s, unguided because the telescope has no Guider, not centered"; imaging.Message != want {
			t.Errorf("the Imaging message is %q, want %q", imaging.Message, want)
		}
	})
}

// A failure of start or afterFrame does not release the target, and
// guidingfailures_test.go tests it.
func TestAFailedStepFailsTheTarget(t *testing.T) {
	cases := []struct{ step, reason string }{
		{"stop", astrophotography.ReasonGuidingFailed},
		{"center", astrophotography.ReasonCenteringFailed},
	}
	for _, c := range cases {
		t.Run(c.step, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				failing := &steps{fails: c.step}
				w.configure = func(o *operator) { o.centering, o.guiding = failing, failing }
				w.put(targetsPath, target("m42", lights(1)))
				w.put(reservationsPath, reservationIn(observatory.ReservationReady))
				w.start(t)
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False "+c.reason })
				if got := w.targetNow(t, "m42").Status.Reservation; got != "" {
					t.Errorf("the target is claimed by Reservation %q, want it released", got)
				}
			})
		})
	}
}

// moveDuringTheFirstFrame edits m42's coordinates while its first frame
// is being exposed.
func (w *runnerWorld) moveDuringTheFirstFrame(t *testing.T) {
	waitUntil(t, w.inFlight(t, "m42"))
	moved := w.targetNow(t, "m42")
	moved.Spec.Coordinates.RA = "05:36:00"
	moved.Metadata.Generation = 2
	w.put(targetsPath, moved)
}

// The frame before the new slew gets no afterFrame, because the slew
// moves the stars on the guide camera anyway.
func TestNewCoordinatesAreCenteredAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		recorded := &steps{}
		w.configure = func(o *operator) { o.centering, o.guiding = recorded, recorded }
		w.put(targetsPath, target("m42", lights(2)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		w.moveDuringTheFirstFrame(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		want := []string{"stop", "center", "start", "stop", "center", "start"}
		if got := recorded.recorded(); !slices.Equal(got, want) {
			t.Errorf("calls %q, want %q", got, want)
		}
	})
}

func TestACenteringThatFailsAfterNewCoordinatesFailsTheTarget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		failing := &steps{fails: "center", passes: 1}
		w.configure = func(o *operator) { o.centering, o.guiding = failing, failing }
		w.put(targetsPath, target("m42", lights(2)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		w.moveDuringTheFirstFrame(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False "+astrophotography.ReasonCenteringFailed })
		if got := w.targetNow(t, "m42").Status.Reservation; got != "" {
			t.Errorf("the target is claimed by Reservation %q, want it released", got)
		}
	})
}
