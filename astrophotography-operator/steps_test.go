package main

// The seams of guiding and centering: where the runner calls them in
// plan 01's order, what their answers do to the Imaging condition, and
// what the runner says when it guides and centers nothing.

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

// steps records each call of the seams, and fails the one a test names.
type steps struct {
	mu    sync.Mutex
	calls []string
	fails string
}

func (s *steps) call(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, name)
	if name == s.fails {
		return errors.New(name + " failed")
	}
	return nil
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

// The runner stops guiding before the slew, centers after it, starts
// guiding before each frame, and calls afterFrame after each frame with
// the count of frames, where dithering goes.
func TestTheRunnerCallsGuidingAndCenteringInPlan01sOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		recorded := &steps{}
		w.configure = func(o *operator) { o.centering, o.guiding = recorded, recorded }
		w.put(targetsPath, target("m42", lights(2)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		imaging, _ := conditions.Find(w.targetNow(t, "m42").Status.Conditions, astrophotography.ConditionImaging)
		if !strings.Contains(imaging.Message, "guided by the test, centered by the test") {
			t.Errorf("Imaging says %q", imaging.Message)
		}
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		want := []string{"stop", "center", "start", "afterFrame 1", "start", "afterFrame 2"}
		if got := recorded.recorded(); !slices.Equal(got, want) {
			t.Errorf("calls %q, want %q", got, want)
		}
	})
}

// With no Guider and no centering, the Imaging condition says that the
// frames are unguided and the mount went to the coordinates with no
// solve.
func TestTheRunnerSaysItGuidesAndCentersNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		imaging, _ := conditions.Find(w.targetNow(t, "m42").Status.Conditions, astrophotography.ConditionImaging)
		if want := "Reservation east-tonight exposes Luminance 10s, unguided because the telescope has no Guider, not centered."; imaging.Message != want {
			t.Errorf("Imaging says %q, want %q", imaging.Message, want)
		}
	})
}

// A step that fails before the slew or in the centering ends the work
// on the target with its reason. A failure to guide keeps the target
// (guidingfailures_test.go).
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
				w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
				w.start(t)
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False "+c.reason })
				if got := w.targetNow(t, "m42").Status.Reservation; got != "" {
					t.Errorf("the target is still claimed by %q", got)
				}
			})
		})
	}
}
