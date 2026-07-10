package main

// The seeding loops: making the boot manifest's Machine and the
// image's Cluster real in the API, tolerantly of the races and
// not-served-yet CRDs of a fleet booting together.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/liken-sh/liken/machine"
)

// machineAPI is a miniature API server for the machine seeding loop:
// it remembers whether the Machine exists, and it can be told to
// answer creates with 404 for a while (the CRD not served yet, the
// ordinary condition of a machine's first minutes) or to fail them
// outright.
type machineAPI struct {
	exists    bool
	notServed int // creates to answer 404 before the CRD "arrives"
	fail      bool
	creates   int
}

func (api *machineAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if !api.exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(&machine.Machine{
				Kind:     "Machine",
				Metadata: machine.ObjectMeta{Name: "node-1", ResourceVersion: "5"},
			})
		case http.MethodPost:
			api.creates++
			if api.fail {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if api.notServed > 0 {
				api.notServed--
				w.WriteHeader(http.StatusNotFound)
				return
			}
			api.exists = true
			w.WriteHeader(http.StatusCreated)
		}
	})
}

func seedMachine() *machine.Machine {
	return &machine.Machine{
		Kind:     "Machine",
		Metadata: machine.ObjectMeta{Name: "node-1"},
		Spec:     machine.MachineSpec{Sysctls: map[string]string{"vm.swappiness": "10"}},
	}
}

func TestEnsureMachineCreatesWhenAbsent(t *testing.T) {
	api := &machineAPI{}
	client := testClient(t, api.handler())
	current, err := ensureMachine(client, seedMachine())
	if err != nil {
		t.Fatal(err)
	}
	if api.creates != 1 {
		t.Errorf("expected one create, got %d", api.creates)
	}
	if current.Metadata.ResourceVersion != "5" {
		t.Errorf("the server's copy comes back, resourceVersion and all: %+v", current.Metadata)
	}
}

func TestEnsureMachineReturnsAnExistingMachine(t *testing.T) {
	api := &machineAPI{exists: true}
	client := testClient(t, api.handler())
	if _, err := ensureMachine(client, seedMachine()); err != nil {
		t.Fatal(err)
	}
	if api.creates != 0 {
		t.Errorf("an existing machine should never be re-created; got %d creates", api.creates)
	}
}

func TestEnsureMachineWaitsOutAnUnservedCRD(t *testing.T) {
	// k3s applies the Machine CRD around the same time it starts this
	// pod, so the first creates can land before the API serves it.
	api := &machineAPI{notServed: 2}
	client := testClient(t, api.handler())
	if _, err := ensureMachine(client, seedMachine()); err != nil {
		t.Fatal(err)
	}
	if api.creates != 3 {
		t.Errorf("the loop retries until the CRD is served: %d creates", api.creates)
	}
}

func TestEnsureMachineReturnsAHardCreateFailure(t *testing.T) {
	api := &machineAPI{fail: true}
	client := testClient(t, api.handler())
	if _, err := ensureMachine(client, seedMachine()); err == nil {
		t.Fatal("a 500 is not a startup condition to wait out; it comes back to the caller")
	}
}

// clusterAPI is a miniature API server for the cluster seeding loop:
// it remembers whether the Cluster exists, and it can be told to
// answer the first create with a conflict (as the real server would
// when another machine's operator created the object first), with
// 404 for a while (the CRD not served yet), or to fail outright.
type clusterAPI struct {
	exists    bool
	conflict  bool
	notServed int // creates to answer 404 before the CRD "arrives"
	fail      bool
	creates   int
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
			if api.fail {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if api.notServed > 0 {
				api.notServed--
				w.WriteHeader(http.StatusNotFound)
				return
			}
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

func TestEnsureClusterWaitsOutAnUnservedCRD(t *testing.T) {
	api := &clusterAPI{notServed: 2}
	client := testClient(t, api.handler())
	if err := ensureCluster(client, seedCluster()); err != nil {
		t.Fatal(err)
	}
	if api.creates != 3 {
		t.Errorf("the loop retries until the CRD is served: %d creates", api.creates)
	}
}

func TestEnsureClusterReturnsAHardCreateFailure(t *testing.T) {
	api := &clusterAPI{fail: true}
	client := testClient(t, api.handler())
	if err := ensureCluster(client, seedCluster()); err == nil {
		t.Fatal("a 500 is not a startup condition to wait out; it comes back to the caller")
	}
}
