package main

// The loops against an API server that refuses writes for a while: each
// pass that fails logs why, and runs again after the backoff, with no
// change in the cluster to wake it.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

func TestAWorkloadThatCannotBeCreatedIsCreatedAfterTheBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		w.api.refuse(true)
		w.api.put(solversPath, solverObject("lab", nil))
		w.api.put(reservationsPath, reservationObject("tonight", "Ready", "frames"))
		settle()
		w.api.refuse(false)
		time.Sleep(retryAfter)
		settle()
		for _, name := range []string{"lab-solver", "tonight-astrophotography"} {
			if _, held := objectOf[deployment](t, w.api, deploymentsPath, "observatory", name); !held {
				t.Errorf("no Deployment %s", name)
			}
		}
		if !strings.Contains(w.logs.String(), "a pass of the Solver loop") || !strings.Contains(w.logs.String(), "a pass of the Reservation loop") {
			t.Errorf("log %q", w.logs.String())
		}
	})
}

// A replace of a Deployment, and a delete of a worker, that the API
// server refuses happen after the backoff.
func TestAReplaceAndADeleteThatFailAreSentAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		w.api.put(solversPath, solverObject("lab", nil))
		w.api.put(reservationsPath, reservationObject("tonight", "Ready", "frames"))
		settle()
		w.api.refuse(true)
		w.api.put(solversPath, solverObject("lab", map[string]any{"indexFiles": map[string]any{"claimName": "indexes-2"}}))
		w.api.put(reservationsPath, reservationObject("tonight", "Released", "frames"))
		settle()
		w.api.refuse(false)
		time.Sleep(retryAfter)
		settle()
		d, _ := objectOf[deployment](t, w.api, deploymentsPath, "observatory", "lab-solver")
		_, worker := objectOf[deployment](t, w.api, deploymentsPath, "observatory", "tonight-astrophotography")
		_, service := objectOf[service](t, w.api, servicesPath, "observatory", "tonight-astrophotography")
		if d.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "indexes-2" || worker || service {
			t.Errorf("the solver mounts %s, and the worker's Deployment %v and Service %v remain",
				d.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName, worker, service)
		}
	})
}

// A Solver that a person deleted keeps its finalizer-free objects until
// the garbage collector removes them, and the loop writes nothing for
// it.
func TestADeletedSolverIsLeftToTheGarbageCollector(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		deleting := solverObject("lab", nil)
		deleting["metadata"].(map[string]any)["deletionTimestamp"] = "2026-10-12T20:00:00Z"
		w.api.put(solversPath, deleting)
		settle()
		if writes := w.api.writes(deploymentsPath); len(writes) != 0 {
			t.Errorf("writes %q", writes)
		}
	})
}

// A pod that goes takes its scheduler message out of the loop's memory,
// so the memory holds only the pods that exist.
func TestTheMemoryOfAPendingPodGoesWithThePod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		loop := newWorkerLoop()
		w := startWorkloads(t, nil)
		w.api.put(reservationsPath, reservationObject("tonight", "Ready", "frames"))
		w.api.put(podsPath, podFixture("tonight-astrophotography-0", "worker-0", workloadLabels(roleWorker, "tonight"), false, "0/1 nodes are available."))
		settle()
		_ = w.o.workerPass(t.Context(), loop)
		w.api.remove(podsPath, "observatory", "tonight-astrophotography-0")
		settle()
		_ = w.o.workerPass(t.Context(), loop)
		if len(loop.pending) != 0 {
			t.Errorf("the loop remembers %v", loop.pending)
		}
	})
}

func TestAPodIsReadyOnlyWhileItStays(t *testing.T) {
	deleting := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)
	ready := []objectCondition{{Type: "Ready", Status: "True"}}
	gone := podObject{Status: podStatus{Conditions: ready}}
	gone.Metadata.DeletionTimestamp = &deleting
	cases := []struct {
		name string
		p    podObject
		want bool
	}{
		{"ready", podObject{Status: podStatus{Conditions: ready}}, true},
		{"being deleted", gone, false},
		{"no condition yet", podObject{}, false},
	}
	for _, c := range cases {
		if got := c.p.ready(); got != c.want {
			t.Errorf("%s: ready %v, want %v", c.name, got, c.want)
		}
	}
}

// A request to a pod that cannot be built or sent is an error, not an
// answer.
func TestAPeerRequestThatCannotBeSentIsAnError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		down := apiservertest.Start(t, http.NotFoundHandler())
		down.SetDown(true)
		o := newOperator(nil, nil, newMetrics("dev"), nil)
		o.workerClient = down.Client()
		cases := []struct {
			name string
			url  string
		}{
			{"a URL that does not parse", "http://[::1"},
			{"a pod that refuses the connection", "http://lab-solver.observatory.svc:8080/indexes"},
		}
		for _, c := range cases {
			if err := o.peer(context.Background(), http.MethodGet, c.url, nil, nil); err == nil {
				t.Errorf("%s: no error", c.name)
			}
		}
	})
}
