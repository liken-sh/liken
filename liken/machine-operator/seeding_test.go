package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// machineAPI is a fake API server for ensureMachine. It responds to
// the first notServed creates with 404, as the API server does before
// it serves the CRD, and to every create with 500 when fail is set.
type machineAPI struct {
	exists    bool
	notServed int
	fail      bool
	creates   int
}

func (fake *machineAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if !fake.exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(&machine.Machine{
				Kind:     "Machine",
				Metadata: api.ObjectMeta{Name: "node-1", ResourceVersion: "5"},
			})
		case http.MethodPost:
			fake.creates++
			if fake.fail {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if fake.notServed > 0 {
				fake.notServed--
				w.WriteHeader(http.StatusNotFound)
				return
			}
			fake.exists = true
			w.WriteHeader(http.StatusCreated)
		}
	})
}

func seedMachine() *machine.Machine {
	return &machine.Machine{
		Kind:     "Machine",
		Metadata: api.ObjectMeta{Name: "node-1"},
		Spec:     machine.MachineSpec{Sysctls: map[string]string{"vm.swappiness": "10"}},
	}
}

func TestEnsureMachineCreatesWhenAbsent(t *testing.T) {
	fake := &machineAPI{}
	client := testClient(t, fake.handler())
	current, err := ensureMachine(client, seedMachine(), nil)
	if err != nil {
		t.Fatalf("ensuring the Machine: %v", err)
	}
	if fake.creates != 1 {
		t.Errorf("creates = %d, want 1", fake.creates)
	}
	if current.Metadata.ResourceVersion != "5" {
		t.Errorf("the returned metadata is %+v, want the server's copy with resourceVersion 5", current.Metadata)
	}
}

func TestEnsureMachineReturnsAnExistingMachine(t *testing.T) {
	fake := &machineAPI{exists: true}
	client := testClient(t, fake.handler())
	if _, err := ensureMachine(client, seedMachine(), nil); err != nil {
		t.Fatalf("ensuring the Machine: %v", err)
	}
	if fake.creates != 0 {
		t.Errorf("for an existing Machine creates = %d, want 0", fake.creates)
	}
}

func TestEnsureMachineWaitsOutAnUnservedCRD(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// k3s applies the Machine CRD at about the time it starts this
		// pod, so the first creates can reach the API before it serves
		// the CRD.
		fake := &machineAPI{notServed: 2}
		client := testClient(t, fake.handler())
		start := time.Now()
		if _, err := ensureMachine(client, seedMachine(), nil); err != nil {
			t.Fatalf("ensuring the Machine: %v", err)
		}
		if fake.creates != 3 {
			t.Errorf("creates = %d, want 3: two refused while the CRD is not served, then one accepted", fake.creates)
		}
		// Each of the two 404s is followed by a pause of 5 seconds plus
		// a random amount of up to half of that.
		if waited := time.Since(start); waited < 10*time.Second || waited >= 15*time.Second {
			t.Errorf("the loop waited %s for two refused creates, want two pauses of 5s to 7.5s", waited)
		}
	})
}

func TestEnsureMachineReturnsAHardCreateFailure(t *testing.T) {
	fake := &machineAPI{fail: true}
	client := testClient(t, fake.handler())
	if _, err := ensureMachine(client, seedMachine(), nil); err == nil {
		t.Fatal("a 500 response returned no error, want the error returned without a retry")
	}
}

// clusterAPI is a fake API server for ensureCluster, like machineAPI.
// When conflict is set, it responds to a create with 409 Conflict, as
// the API server does when another machine created the Cluster first.
type clusterAPI struct {
	exists    bool
	conflict  bool
	notServed int
	fail      bool
	creates   int
}

func (fake *clusterAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			if !fake.exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(&cluster.Cluster{
				Kind:     "Cluster",
				Metadata: api.ObjectMeta{Name: "lab"},
			})
		case r.Method == http.MethodPost:
			fake.creates++
			if fake.fail {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if fake.notServed > 0 {
				fake.notServed--
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if fake.conflict {
				// Another operator created it first, so it exists.
				fake.exists = true
				w.WriteHeader(http.StatusConflict)
				return
			}
			fake.exists = true
			w.WriteHeader(http.StatusCreated)
		}
	})
}

func seedCluster() *cluster.Cluster {
	return &cluster.Cluster{
		Kind:     "Cluster",
		Metadata: api.ObjectMeta{Name: "lab"},
		Spec:     cluster.ClusterSpec{Leaders: []string{"node-1"}},
	}
}

func TestEnsureClusterCreatesWhenAbsent(t *testing.T) {
	fake := &clusterAPI{}
	client := testClient(t, fake.handler())
	if err := ensureCluster(client, seedCluster()); err != nil {
		t.Fatalf("ensuring the Cluster: %v", err)
	}
	if fake.creates != 1 {
		t.Errorf("creates = %d, want 1", fake.creates)
	}
}

func TestEnsureClusterLeavesAnExistingClusterAlone(t *testing.T) {
	fake := &clusterAPI{exists: true}
	client := testClient(t, fake.handler())
	if err := ensureCluster(client, seedCluster()); err != nil {
		t.Fatalf("ensuring the Cluster: %v", err)
	}
	if fake.creates != 0 {
		t.Errorf("for an existing Cluster creates = %d, want 0", fake.creates)
	}
}

func TestEnsureClusterTreatsLosingTheRaceAsSuccess(t *testing.T) {
	fake := &clusterAPI{conflict: true}
	client := testClient(t, fake.handler())
	if err := ensureCluster(client, seedCluster()); err != nil {
		t.Fatalf("a create that loses the race with another machine returned %v, want no error", err)
	}
}

func TestEnsureClusterWaitsOutAnUnservedCRD(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := &clusterAPI{notServed: 2}
		client := testClient(t, fake.handler())
		start := time.Now()
		if err := ensureCluster(client, seedCluster()); err != nil {
			t.Fatalf("ensuring the Cluster: %v", err)
		}
		if fake.creates != 3 {
			t.Errorf("creates = %d, want 3: two refused while the CRD is not served, then one accepted", fake.creates)
		}
		// Each of the two 404s is followed by a pause of 5 seconds plus
		// a random amount of up to half of that.
		if waited := time.Since(start); waited < 10*time.Second || waited >= 15*time.Second {
			t.Errorf("the loop waited %s for two refused creates, want two pauses of 5s to 7.5s", waited)
		}
	})
}

func TestEnsureClusterReturnsAHardCreateFailure(t *testing.T) {
	fake := &clusterAPI{fail: true}
	client := testClient(t, fake.handler())
	if err := ensureCluster(client, seedCluster()); err == nil {
		t.Fatal("a 500 response returned no error, want the error returned without a retry")
	}
}
