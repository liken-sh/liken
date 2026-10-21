package main

// The turn is the machine's entry in the Cluster's ledger, confirmed
// with a read of the API server.

import (
	"encoding/json"
	"testing"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/fakeapi"
)

// ledgerAPI serves a Cluster whose ledger names the given machines.
func ledgerAPI(t *testing.T, machines ...string) *fakeapi.Server {
	t.Helper()
	c := cluster.Cluster{APIVersion: api.APIVersion, Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	for _, name := range machines {
		c.Status.RebootTurns = append(c.Status.RebootTurns, cluster.RebootTurn{Machine: name, MachineResourceVersion: "7"})
	}
	return fakeapi.New(map[string]*fakeapi.Collection{
		kubernetes.ClustersPath: {APIVersion: api.APIVersion, Kind: "Cluster", Items: []map[string]any{toObject(t, c)}},
	})
}

func TestTheTurnIsTheMachinesEntryInTheLedger(t *testing.T) {
	cases := []struct {
		name   string
		ledger []string
		holds  bool
	}{
		{"an entry for the machine", []string{"node-3", "node-4"}, true},
		{"entries for other machines", []string{"node-3"}, false},
		{"an empty ledger", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &reader{client: testClient(t, ledgerAPI(t, c.ledger...))}

			if got := holdsTurn(r, "lab", "node-4"); got != c.holds {
				t.Errorf("holdsTurn = %v, want %v", got, c.holds)
			}
		})
	}
}

// A Cluster that does not read grants no turn.
func TestAnUnreadableClusterGrantsNoTurn(t *testing.T) {
	r := &reader{client: testClient(t, fakeapi.New(map[string]*fakeapi.Collection{}))}

	if holdsTurn(r, "lab", "node-4") {
		t.Error("holdsTurn = true for a Cluster the API server does not hold")
	}
}

// toObject is a value as the fake stores it.
func toObject(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	return object
}
