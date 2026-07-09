package main

// The Node-to-Machine health translation: how the kubelet's own
// account of itself becomes the Machine's NodeHealthy condition.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chrisguidry/liken/kubernetes"
	"github.com/chrisguidry/liken/machine"
)

func TestStorageConditionAllPlaced(t *testing.T) {
	spec := machine.StorageSpec{ClusterState: &machine.StorageRole{Device: "/dev/vda"}}
	status := machine.AllRolesInMemory()
	status.ClusterState = machine.StorageRoleStatus{Backing: machine.BackingPartition, Device: "vda1"}
	c := storageCondition(spec, status)
	if c.Type != "StorageReady" || c.Status != machine.ConditionTrue || c.Reason != "AllRolesPlaced" {
		t.Errorf("got %+v", c)
	}
	if !strings.Contains(c.Message, "clusterState on vda1") {
		t.Errorf("message should name the landing: %q", c.Message)
	}
}

func TestStorageConditionDeclaredButInMemory(t *testing.T) {
	spec := machine.StorageSpec{ClusterState: &machine.StorageRole{Device: "/dev/vda"}}
	c := storageCondition(spec, machine.AllRolesInMemory())
	if c.Status != machine.ConditionFalse || c.Reason != "RolesInMemory" {
		t.Errorf("got %+v", c)
	}
	if !strings.Contains(c.Message, "clusterState") {
		t.Errorf("message should name the role: %q", c.Message)
	}
}

func TestStorageConditionNothingDeclared(t *testing.T) {
	c := storageCondition(machine.StorageSpec{}, machine.AllRolesInMemory())
	if c.Status != machine.ConditionTrue || c.Reason != "NothingDeclared" {
		t.Errorf("got %+v", c)
	}
}

func TestModulesConditionAllHealthy(t *testing.T) {
	c := modulesCondition([]machine.ModuleStatus{
		{Name: "nvidia", State: machine.ModuleLoaded},
		{Name: "loop", State: machine.ModuleBuiltin},
	})
	if c.Type != "ModulesLoaded" || c.Status != machine.ConditionTrue || c.Reason != "AllLoaded" {
		t.Errorf("got %+v", c)
	}
}

func TestModulesConditionNamesTheFix(t *testing.T) {
	c := modulesCondition([]machine.ModuleStatus{
		{Name: "nvidia", State: machine.ModuleLoaded},
		{Name: "nbd", State: machine.ModuleMissing, Message: "not in this image; rebuild the deployment's image, or upgrade to a release built from manifests that declare it"},
	})
	if c.Status != machine.ConditionFalse || c.Reason != "ModulesNotLoaded" {
		t.Errorf("got %+v", c)
	}
	if !strings.Contains(c.Message, "nbd: not in this image; rebuild") {
		t.Errorf("message should carry init's fix: %q", c.Message)
	}
}

func TestModulesConditionNothingDeclared(t *testing.T) {
	c := modulesCondition(nil)
	if c.Status != machine.ConditionTrue || c.Reason != "NothingDeclared" {
		t.Errorf("got %+v", c)
	}
}

func nodeWithReady(status machine.ConditionStatus) *nodeObject {
	n := &nodeObject{}
	n.Status.Conditions = []machine.Condition{
		{Type: "MemoryPressure", Status: "False"},
		{Type: "Ready", Status: status, Message: "kubelet says so"},
	}
	return n
}

func TestAReadyNodeIsHealthy(t *testing.T) {
	c := nodeHealthyCondition(nodeWithReady("True"))
	if c.Status != "True" || c.Reason != "KubeletReady" {
		t.Errorf("got %s/%s", c.Status, c.Reason)
	}
}

func TestANotReadyNodeIsUnhealthy(t *testing.T) {
	c := nodeHealthyCondition(nodeWithReady("Unknown"))
	if c.Status != "False" || c.Reason != "NodeNotReady" {
		t.Errorf("a silent kubelet is not serving this machine: %s/%s", c.Status, c.Reason)
	}
}

func TestANodeWithoutAReadyConditionIsUnhealthy(t *testing.T) {
	c := nodeHealthyCondition(&nodeObject{})
	if c.Status != "False" || c.Reason != "NodeNotReady" {
		t.Errorf("a kubelet that never reported in cannot be assumed healthy: %s/%s", c.Status, c.Reason)
	}
}

// nodeAPI is a miniature API server holding one Node, remembering
// whether it was deleted and what status the operator publishes.
type nodeAPI struct {
	deleted       bool
	publishedPath string
}

func (api *nodeAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			n := &nodeObject{}
			n.Metadata.Name = "node-1"
			n.Metadata.Labels = map[string]string{"node-role.kubernetes.io/control-plane": "true"}
			_ = json.NewEncoder(w).Encode(n)
		case http.MethodDelete:
			api.deleted = true
		case http.MethodPut:
			api.publishedPath = r.URL.Path
		}
	})
}

func TestGetAndDeleteNode(t *testing.T) {
	api := &nodeAPI{}
	client := testClient(t, api.handler())
	node, err := getNode(client, "node-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := node.Metadata.Labels["node-role.kubernetes.io/control-plane"]; !ok {
		t.Errorf("the labels come through: %+v", node.Metadata.Labels)
	}
	if err := deleteNode(client, "node-1"); err != nil || !api.deleted {
		t.Errorf("the delete should land: %v", err)
	}
}

func TestPublishStatusWritesTheStatusSubresource(t *testing.T) {
	api := &nodeAPI{}
	client := testClient(t, api.handler())
	m := &machine.Machine{Metadata: machine.ObjectMeta{Name: "node-1"}}
	if err := kubernetes.PublishStatus(client, m, &machine.MachineStatus{Phase: machine.PhaseReady}); err != nil {
		t.Fatal(err)
	}
	if want := "/apis/liken.sh/v1alpha1/machines/node-1/status"; api.publishedPath != want {
		t.Errorf("status goes through the subresource, got %s", api.publishedPath)
	}
}

// conflictAPI is a miniature API server that answers the first
// `conflicts` status PUTs with 409, serves a fresh copy of the
// machine on GET, and records the body of the PUT that finally
// lands.
type conflictAPI struct {
	conflicts int
	fresh     machine.Machine
	puts      int
	published *machine.Machine
}

func (api *conflictAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(&api.fresh)
			return
		}
		api.puts++
		if api.puts <= api.conflicts {
			w.WriteHeader(http.StatusConflict)
			return
		}
		api.published = &machine.Machine{}
		_ = json.NewDecoder(r.Body).Decode(api.published)
	})
}

func grantCondition(transition time.Time) machine.Condition {
	return machine.Condition{
		Type: machine.RebootApprovedCondition, Status: machine.ConditionTrue,
		Reason: "TurnGranted", LastTransitionTime: transition,
	}
}

func freshMachine(conditions ...machine.Condition) machine.Machine {
	return machine.Machine{
		Metadata: machine.ObjectMeta{Name: "node-1", ResourceVersion: "9"},
		Status:   machine.MachineStatus{Conditions: conditions},
	}
}

func TestPublishOwnStatusSkipsAnUnchangedStatus(t *testing.T) {
	// A settled machine observes the same status pass after pass, and
	// a write that would change nothing should never leave the
	// process: the API server would drop it as a no-op anyway, but
	// only after this machine made every leader consider it.
	api := &conflictAPI{}
	client := testClient(t, api.handler())

	status := &machine.MachineStatus{Phase: machine.PhaseReady, Conditions: []machine.Condition{
		{Type: "Ready", Status: machine.ConditionTrue, Reason: "Reconciled"},
	}}
	before, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	m := &machine.Machine{Metadata: machine.ObjectMeta{Name: "node-1", ResourceVersion: "7"}}
	if err := publishOwnStatus(client, m, status, before); err != nil {
		t.Fatal(err)
	}
	if api.puts != 0 {
		t.Errorf("an unchanged status stays home: %d puts", api.puts)
	}
}

func TestPublishOwnStatusResolvesAConflictWithAFreshRead(t *testing.T) {
	// The conductor granted a turn between this pass's read and its
	// write. The retry must carry the resourceVersion of the fresh
	// copy and adopt the conductor's grant exactly as written,
	// including its transition time, which the rollout's stall clock
	// measures from.
	granted := grantCondition(testNow)
	api := &conflictAPI{conflicts: 1, fresh: freshMachine(granted)}
	client := testClient(t, api.handler())

	m := &machine.Machine{Metadata: machine.ObjectMeta{Name: "node-1", ResourceVersion: "7"}}
	status := &machine.MachineStatus{Phase: machine.PhaseReady, Conditions: []machine.Condition{
		{Type: "Ready", Status: machine.ConditionTrue, Reason: "Reconciled"},
	}}
	if err := publishOwnStatus(client, m, status, nil); err != nil {
		t.Fatal(err)
	}
	if api.published.Metadata.ResourceVersion != "9" {
		t.Errorf("the retry carries the fresh copy's version: %s", api.published.Metadata.ResourceVersion)
	}
	if c := machine.FindCondition(api.published.Status.Conditions, machine.RebootApprovedCondition); c == nil ||
		!c.LastTransitionTime.Equal(testNow) {
		t.Errorf("the conductor's grant rides in from the fresh copy untouched: %+v", c)
	}
	if c := machine.FindCondition(api.published.Status.Conditions, "Ready"); c == nil || c.Status != machine.ConditionTrue {
		t.Errorf("this pass's own observations still win: %+v", c)
	}
}

func TestPublishOwnStatusHonorsAReclaimedGrant(t *testing.T) {
	// The reverse race: this pass carried a grant read before the
	// conductor reclaimed it. The retry must not resurrect it.
	api := &conflictAPI{conflicts: 1, fresh: freshMachine()}
	client := testClient(t, api.handler())

	m := &machine.Machine{Metadata: machine.ObjectMeta{Name: "node-1", ResourceVersion: "7"}}
	status := &machine.MachineStatus{Conditions: []machine.Condition{grantCondition(testNow)}}
	if err := publishOwnStatus(client, m, status, nil); err != nil {
		t.Fatal(err)
	}
	if c := machine.FindCondition(api.published.Status.Conditions, machine.RebootApprovedCondition); c != nil {
		t.Errorf("a reclaimed grant must stay reclaimed: %+v", c)
	}
}

func TestPublishOwnStatusRetriesOnlyOnce(t *testing.T) {
	// A second conflict means the object is changing faster than we
	// can read it; the watch has already queued the event that will
	// trigger the next pass, so the write waits for that.
	api := &conflictAPI{conflicts: 2, fresh: freshMachine()}
	client := testClient(t, api.handler())

	m := &machine.Machine{Metadata: machine.ObjectMeta{Name: "node-1", ResourceVersion: "7"}}
	err := publishOwnStatus(client, m, &machine.MachineStatus{}, nil)
	if !errors.Is(err, kubernetes.ErrConflict) {
		t.Errorf("the second conflict comes back to the caller: %v", err)
	}
	if api.puts != 2 {
		t.Errorf("one retry, no more: %d puts", api.puts)
	}
}
