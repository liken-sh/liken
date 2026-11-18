package main

// These tests run the operator's loops against the fake API server in
// a synctest bubble. No Deployment controller or kubelet runs, so a
// test creates the pods itself and then reads what the operator wrote.

import (
	"io"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

const (
	testWorkerImage = "ghcr.io/liken-sh/astrophotography-operator-worker:test"
	servicesPath    = "/api/v1/services"
)

type operatorWorld struct {
	api   *fakeAPI
	peers *fakePeers
	o     *operator
	logs  *lockedLog
}

// startWorkloads runs the operator's loops until the test ends. change
// modifies the operator before the loops start.
func startWorkloads(t *testing.T, change func(o *operator)) *operatorWorld {
	t.Helper()
	api := startFakeAPI(t)
	client := apiclient.New(apiservertest.Host, api.server.Client(), "")
	recorder := events.New(t.Context(), client, managedBy, events.Options{Instance: operatorPodName, Log: io.Discard})
	logs := &lockedLog{}
	o := newOperator(client, recorder, newMetrics("dev"), logs)
	o.workerImage = testWorkerImage
	peers := newFakePeers()
	o.peerClient = apiservertest.Start(t, peers).Client()
	if change != nil {
		change(o)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		o.run(t.Context(), api.watcher(t))
	}()
	t.Cleanup(func() { <-done })
	return &operatorWorld{api: api, peers: peers, o: o, logs: logs}
}

// settle lets every loop run its pending passes and the Event recorder
// post, then waits until every goroutine in the bubble is blocked, so
// the test reads after the loops' writes.
func settle() {
	time.Sleep(2 * time.Second)
	synctest.Wait()
}

// solverObject builds a Solver as the API server stores it, with the
// CRD's default search settings, and merges spec into its spec.
func solverObject(name string, spec map[string]any) map[string]any {
	full := map[string]any{
		"indexFiles": map[string]any{"claimName": "astrometry-indexes"},
		"search":     map[string]any{"radii": []any{5.0, 10.0}, "blind": true},
	}
	for key, value := range spec {
		full[key] = value
	}
	o := object("observatory", name, nil, full)
	o["metadata"].(map[string]any)["generation"] = 1
	return o
}

// podFixture builds a pod of a workload, with its readiness and, when
// unschedulable is not empty, a scheduler message that says why it
// cannot be scheduled.
func podFixture(name, uid string, labels map[string]string, ready bool, unschedulable string) map[string]any {
	podLabels := map[string]any{}
	for key, value := range labels {
		podLabels[key] = value
	}
	o := object("observatory", name, podLabels, map[string]any{})
	o["metadata"].(map[string]any)["uid"] = uid
	status := "False"
	if ready {
		status = "True"
	}
	conditions := []any{map[string]any{"type": "Ready", "status": status}}
	if unschedulable != "" {
		conditions = append(conditions, map[string]any{"type": "PodScheduled", "status": "False", "message": unschedulable})
	}
	podStatus := map[string]any{"conditions": conditions}
	if ready {
		podStatus["podIP"] = podIPs[uid]
	}
	o["status"] = podStatus
	return o
}

// podIPs maps a pod's UID to its address when it is ready. The
// addresses are in k3s's default pod network.
var podIPs = map[string]string{"pod-0": "10.42.0.10", "pod-1": "10.42.0.11", "worker-0": "10.42.0.20"}

// readySolverPod creates a ready pod of the Solver's workload.
func (w *operatorWorld) readySolverPod(name, uid string) {
	w.api.put(podsPath, podFixture(name, uid, workloadLabels(roleSolver, "lab"), true, ""))
}

func (w *operatorWorld) solver(t *testing.T, name string) astrophotography.Solver {
	t.Helper()
	s, ok := objectOf[astrophotography.Solver](t, w.api, solversPath, "observatory", name)
	if !ok {
		t.Fatalf("the Solver %s does not exist", name)
	}
	return s
}

func readyOf(s astrophotography.Solver) observatory.Condition {
	for _, c := range s.Status.Conditions {
		if c.Type == astrophotography.ConditionReady {
			return c
		}
	}
	return observatory.Condition{}
}

// lockedLog is a log buffer that is safe for the loops' goroutines to
// write while the test reads it.
type lockedLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
