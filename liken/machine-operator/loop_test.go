package main

// These tests run the reconcile loop in a synctest bubble, so the
// retry, sysctl, backstop, and lease timers run on the fake clock.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
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

// fakeFactsWatch is a facts watch that the test controls. The loop
// calls sync once at the start of each pass, so syncs counts passes.
type fakeFactsWatch struct {
	wake  chan struct{}
	syncs atomic.Int64
}

func (w *fakeFactsWatch) watch() *factsWatch {
	return &factsWatch{wake: w.wake, sync: func() error { w.syncs.Add(1); return nil }}
}

// writeAppliedSysctls writes every OS sysctl with its value into the isolated
// sysctl tree, so a pass finds them already applied and records no
// failure.
func writeAppliedSysctls(t *testing.T) {
	t.Helper()
	for name, value := range machine.OSSysctls {
		path := filepath.Join(sysctlRoot, strings.ReplaceAll(name, ".", "/"))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating the sysctl directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			t.Fatalf("writing the sysctl value: %v", err)
		}
	}
}

// testLoop builds a loop over a reader that reads the API server
// directly, with a backstop delay of exactly backstopEvery. Each call
// to its watchFactsTree returns the next of watches, and the returned
// counter counts those calls.
func testLoop(t *testing.T, api http.Handler, watches ...*fakeFactsWatch) (*loop, *atomic.Int64) {
	t.Helper()
	client, _ := passClients(t, api)
	o := metrics.NewOperator(component, machine.Version, []string{machineKind}, watchKinds)
	var opened atomic.Int64
	live := newLiveness()
	live.exit = func(int) { t.Error("the loop ended the process") }
	return &loop{
		objects:        &reader{client: client},
		name:           "node-1",
		clusterName:    "lab",
		fetcher:        &fetcher{},
		heartbeat:      kubernetes.NewHeartbeat("node-1"),
		operator:       o,
		layer:          newMachineMetrics(o, &fetcher{}),
		retry:          retrySchedule{jitter: noJitter},
		live:           live,
		backstopJitter: noJitter,
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
		t.Fatalf("reading the Machine: %v", err)
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

// refusingStatus responds to every status write with a 503 and
// records the time of each write.
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

// A status write that the API server refuses is retried after one
// second, then two, four, and eight, and then every ten seconds. No
// other event starts these passes.
func TestARefusedStatusWriteRetriesOnTheBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		writeAppliedSysctls(t)
		api := &refusingStatus{next: newPassAPI()}
		l, _ := testLoop(t, api, &fakeFactsWatch{wake: make(chan struct{}, 1)})

		runLoop(t, l, 36*time.Second)

		want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}
		if got := api.gaps(); !equalDurations(got, want) {
			t.Errorf("the gaps between status writes were %v, want %v", got, want)
		}
	})
}

// A settled machine runs one pass at the start, then one backstop pass
// five minutes after each pass, and no other pass. Its lease never
// goes longer than renewEvery plus HeartbeatRenewAfter without a
// write.
func TestASettledLoopRunsOnlyTheBackstopAndKeepsItsLeaseFresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		writeAppliedSysctls(t)
		watch := &fakeFactsWatch{wake: make(chan struct{}, 1)}
		api := &leaseLog{next: newPassAPI()}
		l, _ := testLoop(t, api, watch)
		start := time.Now()

		runLoop(t, l, time.Hour-time.Second)

		if passes := watch.syncs.Load(); passes != 12 {
			t.Errorf("the loop ran %d passes in an hour, want 12: the first and one every five minutes", passes)
		}
		if gap := api.longestGap(start, time.Now()); gap > renewEvery+kubernetes.HeartbeatRenewAfter {
			t.Errorf("the lease went %s without a renewal, want at most 12s", gap)
		}
	})
}

// countingMachineReads counts the GET requests for node-1's Machine.
// A loop over a reader with no watches reads it once at the start of
// each pass, so after the test's own first read, the count equals the
// number of passes.
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

// goneMachine responds 404 to a GET of node-1's Machine while gone is
// set, as the API server does after someone deletes the Machine.
type goneMachine struct {
	next http.Handler
	gone atomic.Bool
}

func (h *goneMachine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/machines/node-1") && h.gone.Load() {
		http.Error(w, `{"kind":"Status","code":404}`, http.StatusNotFound)
		return
	}
	h.next.ServeHTTP(w, r)
}

// While the Machine is gone, the loop writes no lease. Renewals resume
// after the first pass that reads the Machine again.
func TestALoopRenewsNoLeaseWhileItsMachineIsGone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		writeAppliedSysctls(t)
		gone := &goneMachine{next: newPassAPI()}
		api := &leaseLog{next: gone}
		facts := &fakeFactsWatch{wake: make(chan struct{}, 1)}
		l, _ := testLoop(t, api, facts)
		startLoop(t, l)
		synctest.Wait()

		gone.gone.Store(true)
		facts.wake <- struct{}{}
		synctest.Wait()
		deleted := time.Now()
		time.Sleep(time.Minute)
		whileGone := api.writesBetween(deleted, time.Now())
		gone.gone.Store(false)
		facts.wake <- struct{}{}
		synctest.Wait()
		back := time.Now()
		time.Sleep(time.Minute)

		if whileGone != 0 || api.writesBetween(back, time.Now()) == 0 {
			t.Errorf("the lease was written %d times while the Machine was gone and %d after it came back, want none and some",
				whileGone, api.writesBetween(back, time.Now()))
		}
	})
}

// The loop writes the lease before its first pass, with this
// machine's Machine as the owner.
func TestTheLeaseIsRenewedFirstAndOwnedByTheMachine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		writeAppliedSysctls(t)
		api := &leaseLog{next: newPassAPI()}
		l, _ := testLoop(t, api, &fakeFactsWatch{wake: make(chan struct{}, 1)})

		runLoop(t, l, time.Second)

		lease := &kubernetes.Lease{}
		if err := l.objects.client.RequestJSON(http.MethodGet, "/apis/coordination.k8s.io/v1/namespaces/liken-system/leases/node-1", nil, lease); err != nil {
			t.Fatalf("reading the lease: %v", err)
		}
		want := []kubernetes.OwnerReference{{APIVersion: "liken.sh/v1alpha1", Kind: "Machine", Name: "node-1", UID: "uid-node-1"}}
		if !slices.Equal(lease.Metadata.OwnerReferences, want) || api.firstWrite() != "lease" {
			t.Errorf("the lease's owners are %+v and the first write was the %s, want %+v and the lease",
				lease.Metadata.OwnerReferences, api.firstWrite(), want)
		}
	})
}

func TestTheUnfinishedLineNamesThreeFailuresAndCountsTheRest(t *testing.T) {
	failures := make([]passFailure, 5)
	for i := range failures {
		failures[i] = passFailure{step: "sysctl " + strconv.Itoa(i), kind: lasting, err: os.ErrNotExist}
	}

	got := describeUnfinished(failures)

	if !strings.Contains(got, "sysctl 2 unfinished") || strings.Contains(got, "sysctl 3") || !strings.HasSuffix(got, "; and 2 more") {
		t.Errorf("the description is %q, want three steps named and \"and 2 more\"", got)
	}
}
