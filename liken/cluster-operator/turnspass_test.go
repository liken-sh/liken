package main

// These tests run whole passes against a fake API server that rejects
// an out-of-date resourceVersion, and check the reboot turns that the
// passes record in the Cluster's status.rebootTurns.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/disruption"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

const (
	clusterStatusPath = kubernetes.ClustersPath + "/lab/status"
	node2StatusPath   = kubernetes.MachinesPath + "/node-2/status"
)

// writes returns the writes to a Machine or the Cluster that the fake
// received, as "METHOD path". Writes to Disruptions and budgets are
// left out, because they grant no turn.
func writes(requests []string) []string {
	return slices.DeleteFunc(slices.Clone(requests), func(request string) bool {
		return strings.HasPrefix(request, http.MethodGet) ||
			strings.Contains(request, kubernetes.DisruptionsPath) || strings.Contains(request, kubernetes.BudgetsPath)
	})
}

// storedTurns returns the machines in the stored status.rebootTurns.
func storedTurns(t *testing.T, client *apiclient.Client) []string {
	t.Helper()
	c, err := kubernetes.GetCluster(client, "lab")
	if err != nil {
		t.Fatalf("reading the Cluster lab: %v", err)
	}
	return turnNames(c.Status.RebootTurns)
}

func TestTheClusterWriteComesBeforeTheGrant(t *testing.T) {
	fake := raceFleet(t)

	reconcileAt(t, leadingReader(t, fake))

	want := []string{"PUT " + clusterStatusPath, "PUT " + node2StatusPath}
	if got := writes(fake.Requests()); !slices.Equal(got, want) {
		t.Errorf("writes = %q, want %q", got, want)
	}
	if got := storedTurns(t, testClient(t, fake)); !slices.Equal(got, []string{"node-2"}) {
		t.Errorf("turns = %q, want node-2", got)
	}
}

// A 409 on the Cluster write means that another copy of the operator
// wrote the status after this pass read it, so the pass writes no
// grant.
func TestAClusterWriteThatConflictsSendsNoGrant(t *testing.T) {
	fake := raceFleet(t)

	reconcileAt(t, leadingReader(t, refusing(fake, http.MethodPut, "/clusters/", http.StatusConflict)))

	if got := writes(fake.Requests()); len(got) != 0 {
		t.Errorf("writes = %q, want none", got)
	}
}

// pruning removes status.rebootTurns from each Cluster status write,
// as the API server does when its Cluster CRD schema has no such field.
func pruning(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == clusterStatusPath {
			var object map[string]any
			_ = json.NewDecoder(r.Body).Decode(&object)
			delete(object["status"].(map[string]any), "rebootTurns")
			body, _ := json.Marshal(object)
			r = httptest.NewRequest(r.Method, r.URL.Path, strings.NewReader(string(body)))
		}
		next.ServeHTTP(w, r)
	})
}

// When the Cluster that the API server returns after a status write has
// no status.rebootTurns, the pass grants nothing and says why in the
// Progressing message.
func TestAnOlderSchemaPreventsEveryGrant(t *testing.T) {
	fake := raceFleet(t)
	client := testClient(t, fake)

	reconcileAt(t, leadingReader(t, pruning(fake)))

	if got := grantHolders(t, client); len(got) != 0 {
		t.Errorf("machines with a grant = %q, want none", got)
	}
	c, err := kubernetes.GetCluster(client, "lab")
	if err != nil {
		t.Fatalf("reading the Cluster lab: %v", err)
	}
	progressing := api.FindCondition(c.Status.Conditions, "Progressing")
	if progressing == nil || !strings.Contains(progressing.Message, "a Cluster schema that stores status.rebootTurns") {
		t.Errorf("Progressing = %+v, want a message that names the schema", progressing)
	}
}

// crash makes one copy of the cluster-operator fail at its first PUT
// to path, as if the process died there. That PUT reaches the API
// server only when applied is true, its response is a 503, and every
// later request fails.
type crash struct {
	next    http.Handler
	path    string
	applied bool

	mu    sync.Mutex
	ended bool
}

func (c *crash) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	ended := c.ended
	c.ended = c.ended || (r.Method == http.MethodPut && r.URL.Path == c.path)
	trigger := !ended && c.ended
	c.mu.Unlock()
	switch {
	case ended:
		w.WriteHeader(http.StatusServiceUnavailable)
	case trigger:
		if c.applied {
			c.next.ServeHTTP(httptest.NewRecorder(), r)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	default:
		c.next.ServeHTTP(w, r)
	}
}

// finishTurn does what machine-operator does after the reboot: it
// deletes the machine's Disruption and removes the SpecConverged
// condition. The grant stays on the Machine for the cluster-operator to
// take back.
func finishTurn(t *testing.T, client *apiclient.Client, name string) {
	t.Helper()
	if err := client.RequestJSON(http.MethodDelete, kubernetes.DisruptionsPath+"/"+disruption.OwnName(name), nil, nil); err != nil {
		t.Fatalf("deleting the Disruption of the turn: %v", err)
	}
	m, err := kubernetes.GetMachine(client, name)
	if err != nil {
		t.Fatalf("reading the Machine: %v", err)
	}
	status := m.Status
	status.Conditions = api.RemoveCondition(slices.Clone(m.Status.Conditions), "SpecConverged")
	if _, err := kubernetes.PublishStatus(client, m, &status); err != nil {
		t.Fatalf("writing the status of the Machine: %v", err)
	}
}

// spentTurn takes node-2 through a whole turn with one copy of the
// cluster-operator: the grant, the reboot, and the revoke. The entry
// for node-2 is still in status.rebootTurns afterward, because only the
// next pass reads the Machine with no grant and removes the entry.
func spentTurn(t *testing.T, fake http.Handler) {
	t.Helper()
	earlier := leadingReader(t, fake)
	reconcileAt(t, earlier)
	finishTurn(t, testClient(t, fake), "node-2")
	reconcileAt(t, earlier)
}

// One copy of the cluster-operator fails at one write of a pass. Then
// a new copy, with no state in memory, passes twice. In each case the
// fleet ends with at most one grant, and status.rebootTurns lists the
// same machines as the grants.
func TestTheNextLeaderSettlesEachCrashPoint(t *testing.T) {
	cases := []struct {
		name    string
		spent   bool
		path    string
		applied bool
		granted []string
		turns   []string
	}{
		{"before the Cluster write", false, clusterStatusPath, false, []string{"node-2"}, []string{"node-2"}},
		{"with the Cluster write in flight, which is applied", false, clusterStatusPath, true, []string{"node-2"}, []string{"node-2"}},
		{"after the Cluster write, before the grant", false, node2StatusPath, false, []string{"node-2"}, []string{"node-2"}},
		{"with the grant in flight, which is applied", false, node2StatusPath, true, []string{"node-2"}, []string{"node-2"}},
		{"after the reclaim, before the entry leaves", true, clusterStatusPath, false, nil, nil},
		{"with the removal in flight, which is applied", true, clusterStatusPath, true, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := raceFleet(t)
			if c.spent {
				spentTurn(t, fake)
			}
			client := testClient(t, fake)

			reconcileAt(t, leadingReader(t, &crash{next: fake, path: c.path, applied: c.applied}))
			b := leadingReader(t, fake)
			reconcileAt(t, b)
			reconcileAt(t, b)

			if got := grantHolders(t, client); !slices.Equal(got, c.granted) {
				t.Errorf("machines with a grant = %q, want %q", got, c.granted)
			}
			if got := storedTurns(t, client); !slices.Equal(got, c.turns) {
				t.Errorf("turns = %q, want %q", got, c.turns)
			}
		})
	}
}

// grantAsAnOlderRelease grants a turn the way a cluster-operator that
// does not write status.rebootTurns does: it sets the RebootApproved
// condition on the Machine and records no entry.
func grantAsAnOlderRelease(t *testing.T, client *apiclient.Client, name string) {
	t.Helper()
	m, err := kubernetes.GetMachine(client, name)
	if err != nil {
		t.Fatalf("reading the Machine: %v", err)
	}
	status := m.Status
	status.Conditions = api.SetCondition(slices.Clone(m.Status.Conditions), api.Condition{
		Type: machine.RebootApprovedCondition, Status: api.ConditionTrue, Reason: "DisruptionBudgetAllows",
	}, passTime)
	if _, err := kubernetes.PublishStatus(client, m, &status); err != nil {
		t.Fatalf("writing the status of the Machine: %v", err)
	}
}

// dropTheRebootTurns writes the Cluster's status with no
// status.rebootTurns, as that older cluster-operator does.
func dropTheRebootTurns(t *testing.T, client *apiclient.Client) {
	t.Helper()
	c, err := kubernetes.GetCluster(client, "lab")
	if err != nil {
		t.Fatalf("reading the Cluster lab: %v", err)
	}
	c.Status.RebootTurns = nil
	if _, err := kubernetes.PublishClusterStatus(client, c); err != nil {
		t.Fatalf("writing the status of the Cluster: %v", err)
	}
}

// A grant that an older release wrote gets an entry in
// status.rebootTurns before any new turn, and gets it again after an
// older release's status write removes the entries. node-1 asks for a
// turn the whole time and gets none, because node-2 fills the budget of
// one.
func TestTheRebootTurnsAdoptTheGrantsOfAnOlderRelease(t *testing.T) {
	fake := raceFleet(t)
	client := testClient(t, fake)
	grantAsAnOlderRelease(t, client, "node-2")
	askForATurn(t, client, "node-1")
	r := leadingReader(t, fake)

	reconcileAt(t, r)
	adopted := storedTurns(t, client)
	dropTheRebootTurns(t, client)
	reconcileAt(t, r)

	if !slices.Equal(adopted, []string{"node-2"}) {
		t.Errorf("turns after the first pass = %q, want node-2", adopted)
	}
	if got := storedTurns(t, client); !slices.Equal(got, []string{"node-2"}) {
		t.Errorf("turns after an older release's write = %q, want node-2", got)
	}
	if got := grantHolders(t, client); !slices.Equal(got, []string{"node-2"}) {
		t.Errorf("machines with a grant = %q, want only node-2", got)
	}
}
