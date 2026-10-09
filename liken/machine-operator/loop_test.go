package main

// These tests run the reconcile loop in a synctest bubble with no
// ticker, so every pass after the first comes from a wake the test can
// name: the retry timer, a watch, or one of the machine's readers.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/metrics"
)

// fakeFactsWatch is a facts watch the test controls. It counts the
// loop's Sync calls, one for each pass.
type fakeFactsWatch struct {
	wake  chan struct{}
	syncs atomic.Int64
}

// watch answers the facts watch the loop holds over the fake.
func (w *fakeFactsWatch) watch() *factsWatch {
	return &factsWatch{wake: w.wake, sync: func() error { w.syncs.Add(1); return nil }}
}

// seedSysctls writes every OS sysctl into the isolated sysctl tree
// with its value, the way a booted machine holds them, so a pass finds
// them converged and leaves no failure behind.
func seedSysctls(t *testing.T) {
	t.Helper()
	for name, value := range machine.OSSysctls {
		path := filepath.Join(sysctlRoot, strings.ReplaceAll(name, ".", "/"))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// testLoop builds a loop over a reader that reads the API server
// directly, with no ticker. Each call to its facts watch opener answers
// the next of watches.
func testLoop(t *testing.T, api http.Handler, watches ...*fakeFactsWatch) (*loop, *atomic.Int64) {
	t.Helper()
	client, _ := passClients(t, api)
	o := metrics.NewOperator(component, machine.Version, []string{machineKind}, watchKinds)
	var opened atomic.Int64
	return &loop{
		objects:     &reader{client: client},
		name:        "node-1",
		clusterName: "lab",
		fetcher:     &fetcher{},
		heartbeat:   kubernetes.NewHeartbeat("node-1"),
		operator:    o,
		layer:       newMachineMetrics(o, &fetcher{}),
		retry:       retrySchedule{jitter: noJitter},
		watchFactsTree: func(context.Context) (*factsWatch, error) {
			return watches[opened.Add(1)-1].watch(), nil
		},
	}, &opened
}

// runLoop runs the loop until the test has waited for d, then stops it
// and waits for it to return.
func runLoop(t *testing.T, l *loop, d time.Duration) {
	t.Helper()
	current, err := l.objects.machine("node-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		l.run(ctx, current)
		close(done)
	}()
	time.Sleep(d)
	cancel()
	<-done
}

// refusingStatus answers every status write with a 503 and records
// when each one came.
type refusingStatus struct {
	next http.Handler
	mu   sync.Mutex
	at   []time.Time
}

func (h *refusingStatus) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/status") {
		h.mu.Lock()
		h.at = append(h.at, time.Now())
		h.mu.Unlock()
		http.Error(w, "etcd is gone", http.StatusServiceUnavailable)
		return
	}
	h.next.ServeHTTP(w, r)
}

func (h *refusingStatus) gaps() []time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	var gaps []time.Duration
	for i := 1; i < len(h.at); i++ {
		gaps = append(gaps, h.at[i].Sub(h.at[i-1]))
	}
	return gaps
}

// A status write that the API server refuses is sent again after one
// second, then two, four, and eight, and then every ten seconds, with
// no ticker and no watch to start the passes.
func TestARefusedStatusWriteRetriesOnTheBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		seedSysctls(t)
		api := &refusingStatus{next: newPassAPI()}
		l, _ := testLoop(t, api, &fakeFactsWatch{wake: make(chan struct{}, 1)})

		runLoop(t, l, 35*time.Second)

		want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}
		if got := api.gaps(); !equalDurations(got, want) {
			t.Errorf("the gaps between status writes were %v, want %v", got, want)
		}
	})
}

// A pass that finishes everything sets no timer, so the loop waits for
// a wake.
func TestASettledLoopRunsNoPassWithoutAWake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		seedSysctls(t)
		watch := &fakeFactsWatch{wake: make(chan struct{}, 1)}
		l, _ := testLoop(t, newPassAPI(), watch)

		runLoop(t, l, time.Hour)

		if passes := watch.syncs.Load(); passes != 1 {
			t.Errorf("the loop ran %d passes in an hour with no wake, want 1", passes)
		}
	})
}

// countingMachineReads counts the reads of node-1's Machine. A loop
// over a reader with no watches reads it once at the start of each
// pass, so after the test's own first read, the count is the count of
// passes.
type countingMachineReads struct {
	next  http.Handler
	reads atomic.Int64
}

func (h *countingMachineReads) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/machines/node-1") {
		h.reads.Add(1)
	}
	h.next.ServeHTTP(w, r)
}

// goneMachine answers node-1's Machine once, for the test's own first
// read, and 404 after that, as the API server does once a person
// deletes the Machine. It counts the writes to the heartbeat lease.
type goneMachine struct {
	next        http.Handler
	reads       atomic.Int64
	leaseWrites atomic.Int64
}

func (h *goneMachine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/machines/node-1") && h.reads.Add(1) > 1:
		http.Error(w, `{"kind":"Status","code":404}`, http.StatusNotFound)
		return
	case r.Method != http.MethodGet && strings.Contains(r.URL.Path, "/leases"):
		h.leaseWrites.Add(1)
	}
	h.next.ServeHTTP(w, r)
}

// A Machine that is gone gets no heartbeat. The lease names the
// Machine as its owner, so a renewal would create a lease that the
// garbage collector deletes again.
func TestALoopRenewsNoLeaseForAMachineThatIsGone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		seedSysctls(t)
		api := &goneMachine{next: newPassAPI()}
		l, _ := testLoop(t, api, &fakeFactsWatch{wake: make(chan struct{}, 1)})

		runLoop(t, l, time.Minute)

		if writes := api.leaseWrites.Load(); writes != 0 {
			t.Errorf("the loop wrote the lease %d times for a Machine that is gone, want none", writes)
		}
	})
}

// A pass with many failures names the first three in its log line and
// counts the rest, because the steps logged each one already.
func TestTheUnfinishedLineNamesThreeFailuresAndCountsTheRest(t *testing.T) {
	failures := make([]passFailure, 5)
	for i := range failures {
		failures[i] = passFailure{step: "sysctl " + strconv.Itoa(i), kind: lasting, err: os.ErrNotExist}
	}

	got := describeUnfinished(failures)

	if !strings.Contains(got, "sysctl 2 unfinished") || strings.Contains(got, "sysctl 3") || !strings.HasSuffix(got, "; and 2 more") {
		t.Errorf("line = %q, want three named and \"and 2 more\"", got)
	}
}
