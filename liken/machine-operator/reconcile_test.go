package main

// These tests run the operator's Node access and its status publish
// against a small fake API server.

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// nodeAPI is a fake API server with one Node. It records whether the
// Node was deleted and the path of the last status PUT.
type nodeAPI struct {
	deleted       bool
	publishedPath string
}

func (fake *nodeAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			n := &nodeObject{}
			n.Metadata.Name = "node-1"
			n.Metadata.Labels = map[string]string{"node-role.kubernetes.io/control-plane": "true"}
			_ = json.NewEncoder(w).Encode(n)
		case http.MethodDelete:
			fake.deleted = true
		case http.MethodPut:
			fake.publishedPath = r.URL.Path
		}
	})
}

func TestGetAndDeleteNode(t *testing.T) {
	fake := &nodeAPI{}
	client := testClient(t, fake.handler())
	node, err := getNode(client, "node-1")
	if err != nil {
		t.Fatalf("reading the Node: %v", err)
	}
	if _, ok := node.Metadata.Labels["node-role.kubernetes.io/control-plane"]; !ok {
		t.Errorf("the Node labels are %+v, want the control-plane label", node.Metadata.Labels)
	}
	if err := deleteNode(client, "node-1", node.Metadata.UID); err != nil || !fake.deleted {
		t.Errorf("deleting the Node returned %v and the server saw a delete: %v, want no error and a delete", err, fake.deleted)
	}
}

func TestPublishStatusWritesTheStatusSubresource(t *testing.T) {
	fake := &nodeAPI{}
	client := testClient(t, fake.handler())
	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-1"}}
	if _, err := kubernetes.PublishStatus(client, m, &machine.MachineStatus{Phase: api.PhaseReady}); err != nil {
		t.Fatalf("publishing the status: %v", err)
	}
	if want := "/apis/liken.sh/v1alpha1/machines/node-1/status"; fake.publishedPath != want {
		t.Errorf("the status was written to %s, want %s", fake.publishedPath, want)
	}
}

// conflictAPI is a fake API server that responds with 409 to the first
// `conflicts` status PUTs, returns a newer copy of the Machine on GET,
// and records the body of the first PUT that succeeds.
type conflictAPI struct {
	conflicts int
	fresh     machine.Machine
	puts      int
	published *machine.Machine
}

func (fake *conflictAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(&fake.fresh)
			return
		}
		fake.puts++
		if fake.puts <= fake.conflicts {
			w.WriteHeader(http.StatusConflict)
			return
		}
		fake.published = &machine.Machine{}
		_ = json.NewDecoder(r.Body).Decode(fake.published)
	})
}

func grantCondition(transition time.Time) api.Condition {
	return api.Condition{
		Type: machine.RebootApprovedCondition, Status: api.ConditionTrue,
		Reason: "TurnGranted", LastTransitionTime: transition,
	}
}

func freshMachine(conditions ...api.Condition) machine.Machine {
	return machine.Machine{
		Metadata: api.ObjectMeta{Name: "node-1", ResourceVersion: "9"},
		Status:   machine.MachineStatus{Conditions: conditions},
	}
}

func TestPublishOwnStatusSkipsAnUnchangedStatus(t *testing.T) {
	// A machine with nothing to change reads the same status on every
	// pass, and each identical write would still make the API server
	// and etcd process a request, so the operator does not send it.
	fake := &conflictAPI{}
	client := testClient(t, fake.handler())

	status := &machine.MachineStatus{Phase: api.PhaseReady, Conditions: []api.Condition{
		{Type: "Ready", Status: api.ConditionTrue, Reason: "Reconciled"},
	}}
	before, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("encoding the status: %v", err)
	}
	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-1", ResourceVersion: "7"}}
	if err := publishOwnStatus(&reader{client: client}, m, status, before); err != nil {
		t.Fatalf("publishing the status: %v", err)
	}
	if fake.puts != 0 {
		t.Errorf("an unchanged status caused %d writes, want none", fake.puts)
	}
}

func TestPublishOwnStatusResolvesAConflictWithAFreshRead(t *testing.T) {
	// cluster-operator granted a reboot turn between this pass's read and
	// its write. The retry must send the resourceVersion of the newer
	// copy and keep the grant exactly as cluster-operator wrote it,
	// because cluster-operator decides that a rollout has stalled from
	// the grant's LastTransitionTime.
	granted := grantCondition(testNow)
	fake := &conflictAPI{conflicts: 1, fresh: freshMachine(granted)}
	client := testClient(t, fake.handler())

	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-1", ResourceVersion: "7"}}
	status := &machine.MachineStatus{Phase: api.PhaseReady, Conditions: []api.Condition{
		{Type: "Ready", Status: api.ConditionTrue, Reason: "Reconciled"},
	}}
	if err := publishOwnStatus(&reader{client: client}, m, status, nil); err != nil {
		t.Fatalf("publishing the status: %v", err)
	}
	if fake.published.Metadata.ResourceVersion != "9" {
		t.Errorf("the retry sent resourceVersion %s, want 9 from the fresh copy", fake.published.Metadata.ResourceVersion)
	}
	if c := api.FindCondition(fake.published.Status.Conditions, machine.RebootApprovedCondition); c == nil ||
		!c.LastTransitionTime.Equal(testNow) {
		t.Errorf("the RebootApproved condition is %+v, want the fresh copy's grant unchanged", c)
	}
	if c := api.FindCondition(fake.published.Status.Conditions, "Ready"); c == nil || c.Status != api.ConditionTrue {
		t.Errorf("the Ready condition is %+v, want True from this pass", c)
	}
}

func TestPublishOwnStatusHonorsAReclaimedGrant(t *testing.T) {
	// cluster-operator reclaimed a grant after this pass read it. The
	// retry must not write the grant again.
	fake := &conflictAPI{conflicts: 1, fresh: freshMachine()}
	client := testClient(t, fake.handler())

	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-1", ResourceVersion: "7"}}
	status := &machine.MachineStatus{Conditions: []api.Condition{grantCondition(testNow)}}
	if err := publishOwnStatus(&reader{client: client}, m, status, nil); err != nil {
		t.Fatalf("publishing the status: %v", err)
	}
	if c := api.FindCondition(fake.published.Status.Conditions, machine.RebootApprovedCondition); c != nil {
		t.Errorf("the RebootApproved condition is %+v, want none because cluster-operator reclaimed it", c)
	}
}

func TestPublishOwnStatusRetriesOnlyOnce(t *testing.T) {
	// After a second conflict the pass stops trying, because the watch
	// has already queued the event of the write that won, and that
	// event starts a pass that publishes.
	fake := &conflictAPI{conflicts: 2, fresh: freshMachine()}
	client := testClient(t, fake.handler())

	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-1", ResourceVersion: "7"}}
	err := publishOwnStatus(&reader{client: client}, m, &machine.MachineStatus{}, nil)
	if !errors.Is(err, apiclient.ErrConflict) {
		t.Errorf("the second conflict returned %v, want apiclient.ErrConflict", err)
	}
	if fake.puts != 2 {
		t.Errorf("the pass sent %d writes, want 2: the first and one retry", fake.puts)
	}
}
