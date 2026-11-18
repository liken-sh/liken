package main

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// storeMidDeactivation rewrites a reservation's status as an older
// operator would have left it partway through deactivation: the first
// step stored as Abort and done, and every later step pending.
func storeMidDeactivation(w *world, name string) {
	w.t.Helper()
	object, _ := w.api.object(kindCollection(observatory.ReservationKind), name)
	next := clone(object)
	status := next["status"].(map[string]any)
	steps := status["steps"].([]any)
	steps = append(steps, map[string]any{"name": "Abort", "state": string(observatory.StepDone),
		"summary": "Found no exposure or slew to abort"})
	for _, step := range observatory.DeactivationSteps[1:] {
		steps = append(steps, map[string]any{"name": string(step), "state": string(observatory.StepPending)})
	}
	status["steps"] = steps
	status["phase"] = string(observatory.ReservationDeactivating)
	status["step"] = string(observatory.StepDeactivation)
	w.api.mu.Lock()
	w.api.store(kindCollection(observatory.ReservationKind), name, next, "MODIFIED")
	w.api.mu.Unlock()
}

// StopWork was once named Abort. When the operator starts and a stored
// reservation is partway through deactivation with its first step named
// Abort, the operator reads that step as StopWork, finishes the
// deactivation, and stores each step once under its current name.
func TestAStoredAbortStepResumesAsStopWork(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.halt()
		storeMidDeactivation(w, "east-tonight")
		w.start()
		r := w.phase("east-tonight", observatory.ReservationReleased, 10*time.Minute)
		var names []observatory.StepName
		for _, s := range r.Status.Steps {
			names = append(names, s.Name)
		}
		want := slices.Concat(observatory.ActivationSteps, observatory.DeactivationSteps)
		if !slices.Equal(names, want) {
			t.Errorf("the steps are %v, want %v", names, want)
		}
		if s := stepOf(r, observatory.StepStopWork); s.State != observatory.StepDone {
			t.Errorf("the StopWork step is %+v, want Done", s)
		}
	})
}
