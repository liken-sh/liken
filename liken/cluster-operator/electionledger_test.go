package main

// This test runs two copies of this program with real elections, and
// pauses the leader between its Cluster write and its grant, the way a
// stalled node pauses a process. The ledger, not the election, keeps
// the budget: pausedleader_test.go holds the proof, and this test shows
// that the rules hold under the election's real timings.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/election/electiontest"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/metrics"
	"k8s.io/client-go/rest"
)

// A pause holds every request to its handler while it is paused, and
// drops a request whose client gave up, the way a stopped process
// sends nothing.
type pause struct {
	next http.Handler

	mu      sync.Mutex
	resumed chan struct{}
}

func (p *pause) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resumed = make(chan struct{})
}

func (p *pause) resume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	close(p.resumed)
	p.resumed = nil
}

func (p *pause) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	resumed := p.resumed
	p.mu.Unlock()
	if resumed != nil {
		select {
		case <-resumed:
		case <-r.Context().Done():
			return
		}
	}
	p.next.ServeHTTP(w, r)
}

// heldGrant holds copy A's first grant write until the release, pauses
// A's election when it catches the write, and records the status the
// API server answers when the write lands.
type heldGrant struct {
	next     http.Handler
	election *pause

	mu       sync.Mutex
	caught   bool
	released chan struct{}
	answered int
}

func (h *heldGrant) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	catch := !h.caught && r.Method == http.MethodPut && r.URL.Path == node2StatusPath
	h.caught = h.caught || catch
	h.mu.Unlock()
	if !catch {
		h.next.ServeHTTP(w, r)
		return
	}
	h.election.stop()
	<-h.released
	answer := httptest.NewRecorder()
	h.next.ServeHTTP(answer, r)
	h.mu.Lock()
	h.answered = answer.Code
	h.mu.Unlock()
	w.WriteHeader(answer.Code)
	_, _ = w.Write(answer.Body.Bytes())
}

func (h *heldGrant) holding() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.caught
}

func (h *heldGrant) answer() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.answered
}

// renewHeartbeats renews each machine's heartbeat Lease every ten
// seconds, as the machine operators do, until ctx ends.
func renewHeartbeats(ctx context.Context, client *apiclient.Client) {
	for {
		for _, name := range []string{"node-1", "node-2", "node-3"} {
			path := "/apis/coordination.k8s.io/v1/namespaces/liken-system/leases/" + name
			lease, err := apiclient.Get[map[string]any](client, path)
			if err != nil {
				continue
			}
			(*lease)["spec"] = map[string]any{"renewTime": time.Now().UTC().Format("2006-01-02T15:04:05.000000Z07:00")}
			body, _ := json.Marshal(*lease)
			_ = client.RequestJSON(http.MethodPut, path, body, nil)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
	}
}

// electionConfig serves an election handler, the way electiontest's
// Config does.
func electionConfig(t *testing.T, handler http.Handler) *rest.Config {
	t.Helper()
	config := apiservertest.Start(t, handler).Config()
	config.ContentType = "application/json"
	return config
}

// runCopy runs one copy of this program: its election, and then its
// sweeps until stop ends. A lost Lease ends stop, as the exit of the
// process would.
func runCopy(t *testing.T, pod string, config *rest.Config, fleet http.Handler, stop context.Context, end context.CancelFunc) {
	t.Helper()
	options := electionOptions(pod, func(string) {})
	options.Exit = func(int) { end() }
	leader, err := election.Start(stop, config, options)
	if err != nil {
		return
	}
	client, watcher := fleetClients(t, fleet)
	o := metrics.NewOperator(component, machine.Version, []string{clusterKind}, watchKinds)
	ticks := time.NewTicker(10 * time.Second)
	defer ticks.Stop()
	run(stop, stop, leader, client, watcher, ticks.C, o, newClusterMetrics(o))
}

func TestTheLedgerHoldsTheBudgetAcrossALeaderChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		budget := &budgetWatch{fake: raceFleet(t)}
		machineOperator := testClient(t, budget)
		lease := electiontest.New(leaseName, leaseNamespace)
		paused := &pause{next: lease}
		held := &heldGrant{next: budget, election: paused, released: make(chan struct{})}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go renewHeartbeats(ctx, machineOperator)

		stopA, endA := context.WithCancel(ctx)
		stopB, endB := context.WithCancel(ctx)
		var copies sync.WaitGroup
		copies.Go(func() { runCopy(t, "liken-cluster-operator-a", electionConfig(t, paused), held, stopA, endA) })
		time.Sleep(time.Second)
		copies.Go(func() { runCopy(t, "liken-cluster-operator-b", lease.Config(t), budget, stopB, endB) })

		// node-2 waits for a turn from the start. A grants it on its first
		// tick after it has judged the heartbeats for
		// HeartbeatStaleAfter.
		time.Sleep(kubernetes.HeartbeatStaleAfter + 15*time.Second)
		if !held.holding() {
			t.Fatal("copy A never sent its grant")
		}
		askForATurn(t, machineOperator, "node-1")

		// B takes the Lease 30 to 41 seconds after A's last renewal, and
		// then judges the heartbeats for HeartbeatStaleAfter before it
		// confirms A's entry. A stays paused until then.
		time.Sleep(2 * time.Minute)
		close(held.released)
		paused.resume()
		synctest.Wait()

		if got := held.answer(); got != http.StatusConflict {
			t.Errorf("A's late grant was answered %d, want 409", got)
		}
		if breaches := budget.exceeded(); len(breaches) > 0 {
			t.Errorf("the budget of one was exceeded: %q", breaches)
		}
		if got := grantHolders(t, machineOperator); !slices.Equal(got, []string{"node-2"}) {
			t.Errorf("machines with a grant = %q, want only node-2", got)
		}
		if holder := lease.Holder(); !strings.HasPrefix(holder, "liken-cluster-operator-b_") {
			t.Errorf("the Lease is held by %q, want copy B", holder)
		}

		endB()
		endA()
		cancel()
		copies.Wait()
	})
}
