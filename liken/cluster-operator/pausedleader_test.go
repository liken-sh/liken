package main

// These tests replay a race between a paused former leader and the
// leader that replaced it. Copy A decides to grant node-2 a reboot
// turn, and one of its writes is held back. node-1 then requests a
// turn, and copy B reconciles the fleet. When A's write goes through, the
// disruption budget of one must still hold. The Cluster's
// status.rebootTurns enforces it (rollout.go).

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

// budgetWatch passes each request to the fake. After each write it
// counts the machines that have a RebootApproved condition or are not
// Ready, and records each time that count exceeds the budget of one.
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

// requestBlocker wraps copy A's API server. It blocks the first matching request
// until the test calls release, as a stopped process or a slow API
// server would. With abandon set, it returns a 504 at once, as a client
// that gave up sees it, and applies the request when the test calls
// release.
type requestBlocker struct {
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

func newRequestBlocker(next http.Handler, method, path string, abandon bool) *requestBlocker {
	return &requestBlocker{next: next, method: method, path: path, abandon: abandon, released: make(chan struct{})}
}

func (g *requestBlocker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

// release lets a blocked request through, or applies an abandoned one.
func (g *requestBlocker) release() {
	close(g.released)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.late != nil {
		late := httptest.NewRequest(g.late.Method, g.late.URL.Path, bytes.NewReader(g.lateBody))
		g.next.ServeHTTP(httptest.NewRecorder(), late)
	}
}

func TestAPausedLeaderCannotExceedTheBudget(t *testing.T) {
	grantPath := kubernetes.MachinesPath + "/node-2/status"
	clusterPath := kubernetes.ClustersPath + "/lab/status"
	cases := []struct {
		name    string
		path    string
		abandon bool
		// bPasses is how many passes B runs before A's write is released.
		bPasses int
		granted []string
	}{
		{"A stops after its Cluster write, before its grant", grantPath, false, 1, []string{"node-2"}},
		{"A's grant is held in the API server and lands after B's passes", grantPath, false, 2, []string{"node-2"}},
		{"A's Cluster write is held and lands after B's write", clusterPath, false, 1, []string{"node-1"}},
		{"A's client abandons its grant and the API server applies it later", grantPath, true, 1, []string{"node-2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				budget := &budgetWatch{fake: raceFleet(t)}
				held := newRequestBlocker(budget, http.MethodPut, c.path, c.abandon)
				a := leadingReader(t, held)
				b := leadingReader(t, budget)
				machineOperator := testClient(t, budget)

				done := make(chan struct{})
				go func() {
					defer close(done)
					reconcileAt(t, a)
				}()
				synctest.Wait()
				askForATurn(t, machineOperator, "node-1")
				for range c.bPasses {
					reconcileAt(t, b)
				}
				held.release()
				<-done
				reconcileAt(t, b)

				if breaches := budget.exceeded(); len(breaches) > 0 {
					t.Errorf("machines with a grant = %q, want at most one", breaches)
				}
				if got := grantHolders(t, machineOperator); !slices.Equal(got, c.granted) {
					t.Errorf("machines with a grant = %q, want %q", got, c.granted)
				}
			})
		})
	}
}
