package main

// These tests cover the grant ledger through whole sweeps against a
// fake API server that checks versions: the order of the writes, a
// sweep that ends at each of its writes, and the grants an older
// cluster-operator leaves.

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

// writes answers each write to a Machine or the Cluster that the fake
// received, as "METHOD path". The reports on the requests and the
// budgets are left out: they grant nothing.
func writes(requests []string) []string {
	return slices.DeleteFunc(slices.Clone(requests), func(request string) bool {
		return strings.HasPrefix(request, http.MethodGet) ||
			strings.Contains(request, kubernetes.DisruptionsPath) || strings.Contains(request, kubernetes.BudgetsPath)
	})
}

// ledgerOf answers the machines that the Cluster's ledger names.
func ledgerOf(t *testing.T, client *apiclient.Client) []string {
	t.Helper()
	c, err := kubernetes.GetCluster(client, "lab")
	if err != nil {
		t.Fatal(err)
	}
	return turnNames(c.Status.RebootTurns)
}

func TestTheClusterWriteComesBeforeTheGrant(t *testing.T) {
	fake := raceFleet(t)

	sweepAt(t, leadingReader(t, fake))

	want := []string{"PUT " + clusterStatusPath, "PUT " + node2StatusPath}
	if got := writes(fake.Requests()); !slices.Equal(got, want) {
		t.Errorf("writes = %q, want %q", got, want)
	}
	if got := ledgerOf(t, testClient(t, fake)); !slices.Equal(got, []string{"node-2"}) {
		t.Errorf("ledger = %q, want node-2", got)
	}
}

// A 409 on the Cluster write means another copy wrote the status since
// this sweep read it, so the sweep sends no grant.
func TestAClusterWriteThatConflictsSendsNoGrant(t *testing.T) {
	fake := raceFleet(t)

	sweepAt(t, leadingReader(t, refusing(fake, http.MethodPut, "/clusters/", http.StatusConflict)))

	if got := writes(fake.Requests()); len(got) != 0 {
		t.Errorf("writes = %q, want none", got)
	}
}

// pruning stands for an API server that serves the Cluster CRD of a
// release without the ledger: it drops status.rebootTurns from each
// Cluster status write, and answers the write without the field.
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

// A Cluster answer without the ledger proves that the served schema
// does not store it, so the sweep grants nothing and says why.
func TestAnOlderSchemaHoldsEveryGrant(t *testing.T) {
	fake := raceFleet(t)
	client := testClient(t, fake)

	sweepAt(t, leadingReader(t, pruning(fake)))

	if got := grantHolders(t, client); len(got) != 0 {
		t.Errorf("machines with a grant = %q, want none", got)
	}
	c, err := kubernetes.GetCluster(client, "lab")
	if err != nil {
		t.Fatal(err)
	}
	progressing := api.FindCondition(c.Status.Conditions, "Progressing")
	if progressing == nil || !strings.Contains(progressing.Message, "a Cluster schema that stores status.rebootTurns") {
		t.Errorf("Progressing = %+v, want a message that names the schema", progressing)
	}
}

// A crash stands in front of copy A's API server and ends A at one
// request. The request lands or not, A never reads an answer, and
// every later request of A fails, as if the process had ended there.
type crash struct {
	next  http.Handler
	path  string
	lands bool

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
		if c.lands {
			c.next.ServeHTTP(httptest.NewRecorder(), r)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	default:
		c.next.ServeHTTP(w, r)
	}
}

// finishTurn does what node-2's machine operator does after the
// reboot: it deletes its request, because nothing is staged, and
// writes the status of a machine that converged. The grant is still
// there for the cluster to reclaim.
func finishTurn(t *testing.T, client *apiclient.Client, name string) {
	t.Helper()
	if err := client.RequestJSON(http.MethodDelete, kubernetes.DisruptionsPath+"/"+disruption.OwnName(name), nil, nil); err != nil {
		t.Fatal(err)
	}
	m, err := kubernetes.GetMachine(client, name)
	if err != nil {
		t.Fatal(err)
	}
	status := m.Status
	status.Conditions = api.RemoveCondition(slices.Clone(m.Status.Conditions), "SpecConverged")
	if _, err := kubernetes.PublishStatus(client, m, &status); err != nil {
		t.Fatal(err)
	}
}

// spentTurn takes node-2 through a whole turn with one copy: the grant,
// the reboot, and the reclaim. The ledger still names node-2, because
// only the next sweep's read shows the Machine at another version.
func spentTurn(t *testing.T, fake http.Handler) {
	t.Helper()
	earlier := leadingReader(t, fake)
	sweepAt(t, earlier)
	finishTurn(t, testClient(t, fake), "node-2")
	sweepAt(t, earlier)
}

// Copy A ends at one write of a sweep, and copy B, with an empty memo,
// sweeps the state A left, twice. Each row is one row of the crash
// table in plan 82.
func TestTheNextLeaderSettlesEachCrashPoint(t *testing.T) {
	cases := []struct {
		name    string
		spent   bool
		path    string
		lands   bool
		granted []string
		ledger  []string
	}{
		{"before the Cluster write", false, clusterStatusPath, false, []string{"node-2"}, []string{"node-2"}},
		{"with the Cluster write in flight, which lands", false, clusterStatusPath, true, []string{"node-2"}, []string{"node-2"}},
		{"after the Cluster write, before the grant", false, node2StatusPath, false, []string{"node-2"}, []string{"node-2"}},
		{"with the grant in flight, which lands", false, node2StatusPath, true, []string{"node-2"}, []string{"node-2"}},
		{"after the reclaim, before the entry leaves", true, clusterStatusPath, false, nil, nil},
		{"with the removal in flight, which lands", true, clusterStatusPath, true, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := raceFleet(t)
			if c.spent {
				spentTurn(t, fake)
			}
			client := testClient(t, fake)

			sweepAt(t, leadingReader(t, &crash{next: fake, path: c.path, lands: c.lands}))
			b := leadingReader(t, fake)
			sweepAt(t, b)
			sweepAt(t, b)

			if got := grantHolders(t, client); !slices.Equal(got, c.granted) {
				t.Errorf("machines with a grant = %q, want %q", got, c.granted)
			}
			if got := ledgerOf(t, client); !slices.Equal(got, c.ledger) {
				t.Errorf("ledger = %q, want %q", got, c.ledger)
			}
		})
	}
}

// grantAsAnOlderRelease writes a grant onto a Machine the way a
// cluster-operator from before the ledger does: with no entry.
func grantAsAnOlderRelease(t *testing.T, client *apiclient.Client, name string) {
	t.Helper()
	m, err := kubernetes.GetMachine(client, name)
	if err != nil {
		t.Fatal(err)
	}
	status := m.Status
	status.Conditions = api.SetCondition(slices.Clone(m.Status.Conditions), api.Condition{
		Type: machine.RebootApprovedCondition, Status: api.ConditionTrue, Reason: "DisruptionBudgetAllows",
	}, sweepNow)
	if _, err := kubernetes.PublishStatus(client, m, &status); err != nil {
		t.Fatal(err)
	}
}

// dropTheLedger writes the Cluster's status the way a cluster-operator
// from before the ledger does: from a status struct with no ledger.
func dropTheLedger(t *testing.T, client *apiclient.Client) {
	t.Helper()
	c, err := kubernetes.GetCluster(client, "lab")
	if err != nil {
		t.Fatal(err)
	}
	c.Status.RebootTurns = nil
	if _, err := kubernetes.PublishClusterStatus(client, c); err != nil {
		t.Fatal(err)
	}
}

// A grant from an older release enters the ledger before any new turn,
// and enters it again after an older release's status write removes
// the ledger. node-1 asks the whole time, and never takes a turn while
// node-2 holds the budget of one.
func TestTheLedgerAdoptsTheGrantsOfAnOlderRelease(t *testing.T) {
	fake := raceFleet(t)
	client := testClient(t, fake)
	grantAsAnOlderRelease(t, client, "node-2")
	askForATurn(t, client, "node-1")
	r := leadingReader(t, fake)

	sweepAt(t, r)
	adopted := ledgerOf(t, client)
	dropTheLedger(t, client)
	sweepAt(t, r)

	if !slices.Equal(adopted, []string{"node-2"}) {
		t.Errorf("ledger after the first sweep = %q, want node-2", adopted)
	}
	if got := ledgerOf(t, client); !slices.Equal(got, []string{"node-2"}) {
		t.Errorf("ledger after an older release's write = %q, want node-2", got)
	}
	if got := grantHolders(t, client); !slices.Equal(got, []string{"node-2"}) {
		t.Errorf("machines with a grant = %q, want only node-2", got)
	}
}
