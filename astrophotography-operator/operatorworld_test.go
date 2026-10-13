package main

// The operator's loops against the fake API server and the fake pods.
// Each test runs in a synctest bubble, plays the Deployment controller
// and the kubelet by putting pods into the fake, and reads what the
// operator wrote.

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
// edits the operator before its loops start.
func startWorkloads(t *testing.T, change func(o *operator)) *operatorWorld {
	t.Helper()
	api := startFakeAPI(t)
	client := apiclient.New(apiservertest.Host, api.server.Client(), "")
	recorder := events.New(t.Context(), client, managedBy, events.Options{Instance: operatorPodName, Log: io.Discard})
	logs := &lockedLog{}
	o := newOperator(client, recorder, newMetrics("dev"), logs)
	o.workerImage = testWorkerImage
	peers := newFakePeers()
	o.workerClient = apiservertest.Start(t, peers).Client()
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

// settle lets every loop run its passes, and the Event recorder post.
// The Wait at the end orders every write of the loops before the
// test's reads.
func settle() {
	time.Sleep(2 * time.Second)
	synctest.Wait()
}

// solverObject builds the lab's Solver as the API server stores it,
// with the CRD's default search, and a spec to merge in.
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

// podFixture builds a pod of a workload, with its readiness and the
// scheduler's message, if any.
func podFixture(name, uid string, labels map[string]string, ready bool, unschedulable string) map[string]any {
	held := map[string]any{}
	for key, value := range labels {
		held[key] = value
	}
	o := object("observatory", name, held, map[string]any{})
	o["metadata"].(map[string]any)["uid"] = uid
	status := "False"
	if ready {
		status = "True"
	}
	conditions := []any{map[string]any{"type": "Ready", "status": status}}
	if unschedulable != "" {
		conditions = append(conditions, map[string]any{"type": "PodScheduled", "status": "False", "message": unschedulable})
	}
	o["status"] = map[string]any{"conditions": conditions}
	return o
}

// readyPod puts a ready pod of one Solver.
func (w *operatorWorld) readySolverPod(name, uid string) {
	w.api.put(podsPath, podFixture(name, uid, workloadLabels(roleSolver, "lab"), true, ""))
}

func (w *operatorWorld) solver(t *testing.T, name string) astrophotography.Solver {
	t.Helper()
	s, held := objectOf[astrophotography.Solver](t, w.api, solversPath, "observatory", name)
	if !held {
		t.Fatalf("no Solver %s", name)
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

// lockedLog lets the loops log from their goroutines while a test
// reads the log.
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
