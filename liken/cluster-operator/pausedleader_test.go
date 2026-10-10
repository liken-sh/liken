package main

// These tests replay the race between a paused former leader and the
// leader that took its place. Copy A decides to grant node-2 a reboot
// turn, and its write is held. node-1 then asks for a turn, and copy B
// sweeps the fleet. When A's write lands at last, the disruption
// budget of one must still hold. The ledger in the Cluster's status
// (rollout.go) is what keeps it.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/fakeapi"
	"github.com/liken-sh/liken/liken/machine"
)

// A budgetWatch passes each request to the fake, and after each write
// counts the machines that hold a reboot grant or are not Ready. It
// records every count above the budget of one, and every moment two
// leaders are out at once.
type budgetWatch struct {
	fake *fakeapi.Server

	mu       sync.Mutex
	breaches []string
}

func (b *budgetWatch) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.fake.ServeHTTP(w, r)
	if r.Method == http.MethodGet {
		return
	}
	list := httptest.NewRecorder()
	b.fake.ServeHTTP(list, httptest.NewRequest(http.MethodGet, kubernetes.MachinesPath, nil))
	var machines struct{ Items []machine.Machine }
	_ = json.Unmarshal(list.Body.Bytes(), &machines)
	var out []string
	for _, m := range machines.Items {
		if api.FindCondition(m.Status.Conditions, machine.RebootApprovedCondition) != nil || m.Status.Phase != api.PhaseReady {
			out = append(out, m.Metadata.Name)
		}
	}
	if len(out) > 1 {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.breaches = append(b.breaches, r.Method+" "+r.URL.Path+" left out: "+strings.Join(out, ", "))
	}
}

func (b *budgetWatch) exceeded() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.breaches)
}

// A gate stands in front of copy A's API server. It holds the first
// request that matches, the way a stopped process or a slow API server
// holds a write, until the test releases it. An abandoning gate
// answers the request at once with a timeout, the way a client that
// gave up sees it, and still applies the request at the release.
type gate struct {
	next    http.Handler
	method  string
	path    string
	abandon bool

	mu       sync.Mutex
	caught   bool
	released chan struct{}
	late     *http.Request
	lateBody []byte
}

func newGate(next http.Handler, method, path string, abandon bool) *gate {
	return &gate{next: next, method: method, path: path, abandon: abandon, released: make(chan struct{})}
}

func (g *gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	catch := !g.caught && r.Method == g.method && r.URL.Path == g.path
	g.caught = g.caught || catch
	g.mu.Unlock()
	if !catch {
		g.next.ServeHTTP(w, r)
		return
	}
	if g.abandon {
		body, _ := io.ReadAll(r.Body)
		g.mu.Lock()
		g.late, g.lateBody = r.Clone(r.Context()), body
		g.mu.Unlock()
		w.WriteHeader(http.StatusGatewayTimeout)
		_, _ = w.Write([]byte(`{"kind":"Status","status":"Failure","reason":"Timeout","code":504}`))
		return
	}
	<-g.released
	g.next.ServeHTTP(w, r)
}

// release lets a held request through, or applies an abandoned one.
func (g *gate) release() {
	close(g.released)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.late != nil {
		late := httptest.NewRequest(g.late.Method, g.late.URL.Path, bytes.NewReader(g.lateBody))
		g.next.ServeHTTP(httptest.NewRecorder(), late)
	}
}

// Copy A stops or is slowed at one write, copy B sweeps while A waits,
// A's write is released, and B sweeps again. The budget of one holds
// at every write, and one machine holds the turn.
func TestAPausedLeaderCannotExceedTheBudget(t *testing.T) {
	grantPath := kubernetes.MachinesPath + "/node-2/status"
	clusterPath := kubernetes.ClustersPath + "/lab/status"
	cases := []struct {
		name    string
		path    string
		abandon bool
		// bSweeps is how many sweeps B runs before A's write goes on.
		bSweeps int
		granted []string
	}{
		{"A stops after its Cluster write, before its grant", grantPath, false, 1, []string{"node-2"}},
		{"A's grant is held in the API server and lands after B's sweeps", grantPath, false, 2, []string{"node-2"}},
		{"A's Cluster write is held and lands after B's write", clusterPath, false, 1, []string{"node-1"}},
		{"A's client abandons its grant and the API server applies it later", grantPath, true, 1, []string{"node-2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				budget := &budgetWatch{fake: raceFleet(t)}
				held := newGate(budget, http.MethodPut, c.path, c.abandon)
				a := leadingReader(t, held)
				b := leadingReader(t, budget)
				machineOperator := testClient(t, budget)

				done := make(chan struct{})
				go func() {
					defer close(done)
					sweepAt(t, a)
				}()
				synctest.Wait()
				askForATurn(t, machineOperator, "node-1")
				for range c.bSweeps {
					sweepAt(t, b)
				}
				held.release()
				<-done
				sweepAt(t, b)

				if breaches := budget.exceeded(); len(breaches) > 0 {
					t.Errorf("the budget of one was exceeded: %q", breaches)
				}
				if got := grantHolders(t, machineOperator); !slices.Equal(got, c.granted) {
					t.Errorf("machines with a grant = %q, want %q", got, c.granted)
				}
			})
		})
	}
}
