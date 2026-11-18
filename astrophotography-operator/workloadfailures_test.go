package main

// Tests of the workload loops while the API server refuses writes. A
// pass that fails logs the reason and runs again after retryAfter, even
// when nothing in the cluster changes.

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
			if _, found := objectOf[deployment](t, w.api, deploymentsPath, "observatory", name); !found {
				t.Errorf("the Deployment %s does not exist after the API server accepts writes again", name)
			}
		}
		if !strings.Contains(w.logs.String(), "the Solver loop: ") || !strings.Contains(w.logs.String(), "the Reservation loop: ") {
			t.Errorf("the log is %q, want a line for the Solver loop and one for the Reservation loop", w.logs.String())
		}
	})
}

// A Deployment replace and a worker delete that the API server refuses
// are sent again after the backoff.
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
			t.Errorf("the solver mounts %s, and the worker Deployment exists is %v and the worker Service exists is %v, want the new claim and neither worker object",
				d.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName, worker, service)
		}
	})
}

// The garbage collector deletes a deleted Solver's Deployment and
// Service through their owner references, so the loop writes nothing
// for them.
func TestADeletedSolverIsLeftToTheGarbageCollector(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorkloads(t, nil)
		deleting := solverObject("lab", nil)
		deleting["metadata"].(map[string]any)["deletionTimestamp"] = "2026-10-12T20:00:00Z"
		w.api.put(solversPath, deleting)
		settle()
		if writes := w.api.writes(deploymentsPath); len(writes) != 0 {
			t.Errorf("the loop wrote %q to the Deployments, want no writes", writes)
		}
	})
}

func TestTheSchedulerMessageOfADeletedPodIsForgotten(t *testing.T) {
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
			t.Errorf("the loop still remembers the scheduler messages %v, want none", loop.pending)
		}
	})
}

func TestAPodBeingDeletedIsNotReady(t *testing.T) {
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
			t.Errorf("%s: ready() is %v, want %v", c.name, got, c.want)
		}
	}
}

func TestAPeerRequestThatCannotBeSentIsAnError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		down := apiservertest.Start(t, http.NotFoundHandler())
		down.SetDown(true)
		o := newOperator(nil, nil, newMetrics("dev"), nil)
		o.peerClient = down.Client()
		cases := []struct {
			name string
			url  string
		}{
			{"a URL that does not parse", "http://[::1"},
			{"a pod that refuses the connection", "http://lab-solver.observatory.svc:8080/indexes"},
		}
		for _, c := range cases {
			if err := o.peer(context.Background(), http.MethodGet, c.url, nil, nil); err == nil {
				t.Errorf("%s: peer returned no error, want one", c.name)
			}
		}
	})
}
