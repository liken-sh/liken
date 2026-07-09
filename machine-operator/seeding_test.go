package main

// The seeding loops: making the boot manifest's Machine and the
// image's Cluster real in the API, tolerantly of the races and
// not-served-yet CRDs of a fleet booting together.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/chrisguidry/liken/machine"
)

// clusterAPI is a miniature API server for the seeding loop: it
// remembers whether the Cluster exists and can be told to answer the
// first create with a conflict, as the real server would when another
// machine's operator created the object first.
type clusterAPI struct {
	exists   bool
	conflict bool
	creates  int
}

func (api *clusterAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			if !api.exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(&machine.Cluster{
				Kind:     "Cluster",
				Metadata: machine.ObjectMeta{Name: "lab"},
			})
		case r.Method == http.MethodPost:
			api.creates++
			if api.conflict {
				// Someone else's create landed first; the object
				// exists now no matter who made it.
				api.exists = true
				w.WriteHeader(http.StatusConflict)
				return
			}
			api.exists = true
			w.WriteHeader(http.StatusCreated)
		}
	})
}

func seedCluster() *machine.Cluster {
	return &machine.Cluster{
		Kind:     "Cluster",
		Metadata: machine.ObjectMeta{Name: "lab"},
		Spec:     machine.ClusterSpec{Leaders: []string{"node-1"}},
	}
}

func TestEnsureClusterCreatesWhenAbsent(t *testing.T) {
	api := &clusterAPI{}
	client := testClient(t, api.handler())
	if err := ensureCluster(client, seedCluster()); err != nil {
		t.Fatal(err)
	}
	if api.creates != 1 {
		t.Errorf("expected one create, got %d", api.creates)
	}
}

func TestEnsureClusterLeavesAnExistingClusterAlone(t *testing.T) {
	api := &clusterAPI{exists: true}
	client := testClient(t, api.handler())
	if err := ensureCluster(client, seedCluster()); err != nil {
		t.Fatal(err)
	}
	if api.creates != 0 {
		t.Errorf("an existing cluster should never be re-created; got %d creates", api.creates)
	}
}

func TestEnsureClusterTreatsLosingTheRaceAsSuccess(t *testing.T) {
	api := &clusterAPI{conflict: true}
	client := testClient(t, api.handler())
	if err := ensureCluster(client, seedCluster()); err != nil {
		t.Fatalf("a lost race is a seeded cluster: %v", err)
	}
}
