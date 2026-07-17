package main

// The reconcile loop's writes against a miniature API server: the
// operator's access to its own Node object, and the status publish
// that resolves conflicts without discarding a pass's observations.

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// nodeAPI is a miniature API server holding one Node, remembering
// whether it was deleted and what status the operator publishes.
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
		t.Fatal(err)
	}
	if _, ok := node.Metadata.Labels["node-role.kubernetes.io/control-plane"]; !ok {
		t.Errorf("the labels come through: %+v", node.Metadata.Labels)
	}
	if err := deleteNode(client, "node-1"); err != nil || !fake.deleted {
		t.Errorf("the delete should land: %v", err)
	}
}

func TestPublishStatusWritesTheStatusSubresource(t *testing.T) {
	fake := &nodeAPI{}
	client := testClient(t, fake.handler())
	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-1"}}
	if err := kubernetes.PublishStatus(client, m, &machine.MachineStatus{Phase: api.PhaseReady}); err != nil {
		t.Fatal(err)
	}
	if want := "/apis/liken.sh/v1alpha1/machines/node-1/status"; fake.publishedPath != want {
		t.Errorf("status goes through the subresource, got %s", fake.publishedPath)
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
	// A settled machine observes the same status pass after pass, and
	// a write that would change nothing should never leave the
	// process: the API server would drop it as a no-op anyway, but
	// only after this machine made every leader consider it.
	fake := &conflictAPI{}
	client := testClient(t, fake.handler())

	status := &machine.MachineStatus{Phase: api.PhaseReady, Conditions: []api.Condition{
		{Type: "Ready", Status: api.ConditionTrue, Reason: "Reconciled"},
	}}
	before, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-1", ResourceVersion: "7"}}
	if err := publishOwnStatus(client, m, status, before); err != nil {
		t.Fatal(err)
	}
	if fake.puts != 0 {
		t.Errorf("an unchanged status stays home: %d puts", fake.puts)
	}
}

func TestPublishOwnStatusResolvesAConflictWithAFreshRead(t *testing.T) {
	// The conductor granted a turn between this pass's read and its
	// write. The retry must carry the resourceVersion of the fresh
	// copy and adopt the conductor's grant exactly as written,
	// including its transition time, which the rollout's stall clock
	// measures from.
	granted := grantCondition(testNow)
	fake := &conflictAPI{conflicts: 1, fresh: freshMachine(granted)}
	client := testClient(t, fake.handler())

	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-1", ResourceVersion: "7"}}
	status := &machine.MachineStatus{Phase: api.PhaseReady, Conditions: []api.Condition{
		{Type: "Ready", Status: api.ConditionTrue, Reason: "Reconciled"},
	}}
	if err := publishOwnStatus(client, m, status, nil); err != nil {
		t.Fatal(err)
	}
	if fake.published.Metadata.ResourceVersion != "9" {
		t.Errorf("the retry carries the fresh copy's version: %s", fake.published.Metadata.ResourceVersion)
	}
	if c := api.FindCondition(fake.published.Status.Conditions, machine.RebootApprovedCondition); c == nil ||
		!c.LastTransitionTime.Equal(testNow) {
		t.Errorf("the conductor's grant rides in from the fresh copy untouched: %+v", c)
	}
	if c := api.FindCondition(fake.published.Status.Conditions, "Ready"); c == nil || c.Status != api.ConditionTrue {
		t.Errorf("this pass's own observations still win: %+v", c)
	}
}

func TestPublishOwnStatusHonorsAReclaimedGrant(t *testing.T) {
	// The reverse race: this pass carried a grant read before the
	// conductor reclaimed it. The retry must not resurrect it.
	fake := &conflictAPI{conflicts: 1, fresh: freshMachine()}
	client := testClient(t, fake.handler())

	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-1", ResourceVersion: "7"}}
	status := &machine.MachineStatus{Conditions: []api.Condition{grantCondition(testNow)}}
	if err := publishOwnStatus(client, m, status, nil); err != nil {
		t.Fatal(err)
	}
	if c := api.FindCondition(fake.published.Status.Conditions, machine.RebootApprovedCondition); c != nil {
		t.Errorf("a reclaimed grant must stay reclaimed: %+v", c)
	}
}

func TestPublishOwnStatusRetriesOnlyOnce(t *testing.T) {
	// A second conflict means the object is changing faster than we
	// can read it; the watch has already queued the event that will
	// trigger the next pass, so the write waits for that.
	fake := &conflictAPI{conflicts: 2, fresh: freshMachine()}
	client := testClient(t, fake.handler())

	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-1", ResourceVersion: "7"}}
	err := publishOwnStatus(client, m, &machine.MachineStatus{}, nil)
	if !errors.Is(err, kubernetes.ErrConflict) {
		t.Errorf("the second conflict comes back to the caller: %v", err)
	}
	if fake.puts != 2 {
		t.Errorf("one retry, no more: %d puts", fake.puts)
	}
}
