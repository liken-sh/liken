package main

import (
	"encoding/json"
	"testing"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/fakeapi"
)

// rebootTurnsAPI serves a Cluster whose status.rebootTurns has an entry
// for each of the machines.
func rebootTurnsAPI(t *testing.T, machines ...string) *fakeapi.Server {
	t.Helper()
	c := cluster.Cluster{APIVersion: api.APIVersion, Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}}
	for _, name := range machines {
		c.Status.RebootTurns = append(c.Status.RebootTurns, cluster.RebootTurn{Machine: name, MachineResourceVersion: "7"})
	}
	return fakeapi.New(map[string]*fakeapi.Collection{
		kubernetes.ClustersPath: {APIVersion: api.APIVersion, Kind: "Cluster", Items: []map[string]any{toObject(t, c)}},
	})
}

func TestTheMachineHasATurnWhenTheClusterHasAnEntryForIt(t *testing.T) {
	cases := []struct {
		name    string
		entries []string
		holds   bool
	}{
		{"an entry for the machine", []string{"node-3", "node-4"}, true},
		{"entries for other machines", []string{"node-3"}, false},
		{"no entries", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &reader{client: testClient(t, rebootTurnsAPI(t, c.entries...))}

			if got := holdsTurn(r, "lab", "node-4"); got != c.holds {
				t.Errorf("holdsTurn = %v, want %v", got, c.holds)
			}
		})
	}
}

func TestAnUnreadableClusterGrantsNoTurn(t *testing.T) {
	r := &reader{client: testClient(t, fakeapi.New(map[string]*fakeapi.Collection{}))}

	if holdsTurn(r, "lab", "node-4") {
		t.Error("holdsTurn = true for a Cluster that the API server does not have, want false")
	}
}

// toObject converts v to the generic form that the fake server
// stores.
func toObject(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling the object: %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("unmarshaling the object: %v", err)
	}
	return object
}
