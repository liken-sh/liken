package main

// The Node-to-Machine health translation: how the kubelet's own
// account of itself becomes the Machine's NodeHealthy condition.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

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
	if err := publishStatus(client, m, &machine.MachineStatus{Phase: machine.PhaseReady}); err != nil {
		t.Fatal(err)
	}
	if want := "/apis/liken.sh/v1alpha1/machines/node-1/status"; api.publishedPath != want {
		t.Errorf("status goes through the subresource, got %s", api.publishedPath)
	}
}
