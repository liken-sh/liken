package main

// Tests for the demotion cleanup decision. A machine whose
// derived role is follower, but whose Node object still claims
// control-plane, was demoted. The leftover Node, with its etcd
// membership, must be removed automatically, because a dead etcd
// member still counts toward the quorum size and breaks the
// majority math the next time an actual leader reboots.

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

func TestDemotionCleanupFiresForADemotedFollower(t *testing.T) {
	labels := map[string]string{"node-role.kubernetes.io/control-plane": "true"}
	d := decideDemotion(api.RoleFollower, labels, machine.RebootAuto, turnGranted)
	if !d.cleanup {
		t.Error("a follower with a control-plane Node needs cleanup")
	}
	if d.condition.Status != "False" || d.condition.Reason != "DemotionRebooting" {
		t.Errorf("got %+v", d.condition)
	}
}

func TestDemotionCleanupWaitsUnderManualPolicy(t *testing.T) {
	labels := map[string]string{"node-role.kubernetes.io/etcd": "true"}
	d := decideDemotion(api.RoleFollower, labels, machine.RebootManual, turnGranted)
	if d.cleanup {
		t.Error("cleanup deletes the Node and reboots; Manual policy must gate it")
	}
	if d.condition.Status != "False" || d.condition.Reason != "DemotionPending" {
		t.Errorf("got %+v", d.condition)
	}
}

func TestACleanFollowerNeedsNothing(t *testing.T) {
	d := decideDemotion(api.RoleFollower, map[string]string{"kubernetes.io/hostname": "node-4"}, machine.RebootAuto, turnGranted)
	if d.cleanup || d.condition.Status != "True" {
		t.Errorf("got %+v", d)
	}
}

func TestALeaderIsAlwaysCurrent(t *testing.T) {
	// A leader's control-plane labels are exactly right. A leader
	// still starting up, with labels not yet set, is k3s's job, not
	// the operator's.
	labels := map[string]string{"node-role.kubernetes.io/control-plane": "true"}
	d := decideDemotion(api.RoleLeader, labels, machine.RebootAuto, turnGranted)
	if d.cleanup || d.condition.Status != "True" {
		t.Errorf("got %+v", d)
	}
}

func TestDemotionWaitsForItsRebootTurn(t *testing.T) {
	labels := map[string]string{"node-role.kubernetes.io/control-plane": "true"}
	d := decideDemotion(api.RoleFollower, labels, machine.RebootAuto, turnAwaiting)
	if d.cleanup {
		t.Error("no turn granted means no Node deletion and no reboot")
	}
	if d.condition.Reason != "AwaitingTurn" {
		t.Errorf("got %+v", d.condition)
	}
}

// The demotion deletes the Node only while it is the instance the pass
// read. The delete carries the UID as a precondition, so a Node that
// k3s registered again after the read, under the same name, is kept:
// the API server answers 409 and deletes nothing.
func TestTheDemotionDeletesOnlyTheNodeItRead(t *testing.T) {
	var sent struct {
		Kind          string `json:"kind"`
		Preconditions struct {
			UID string `json:"uid"`
		} `json:"preconditions"`
	}
	var method, path string
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&sent)
		_, _ = w.Write([]byte(`{}`))
	}))

	if err := deleteNode(client, "node-2", "uid-read"); err != nil {
		t.Fatal(err)
	}

	if method != http.MethodDelete || path != "/api/v1/nodes/node-2" || sent.Kind != "DeleteOptions" || sent.Preconditions.UID != "uid-read" {
		t.Errorf("sent %s %s with %+v, want a DELETE of node-2 whose precondition is uid-read", method, path, sent)
	}
}

// demotingNode answers the stale control-plane Node a demotion cleans
// up, and a server that records each DELETE and answers it with status.
func demotingNode(t *testing.T, status int) (*nodeObject, *[]string, http.Handler) {
	t.Helper()
	node := &nodeObject{}
	node.Metadata.Name, node.Metadata.UID = "node-2", "uid-read"
	var deleted []string
	return node, &deleted, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted = append(deleted, r.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{}`))
	})
}

// The cleanup writes the reboot intent before it deletes the Node,
// because the delete kills this pod, and the reboot must already be
// asked for when it does.
func TestTheDemotionAsksForTheRebootAndDeletesTheNode(t *testing.T) {
	node, deleted, handler := demotingNode(t, http.StatusOK)
	runDir := t.TempDir()
	out := &passOutcome{}

	c := carryOutDemotion(testClient(t, handler), runDir, node, demotion{cleanup: true, condition: api.Condition{Reason: "DemotionRebooting"}}, out)

	intent, err := machine.ReadRebootIntent(runDir)
	if err != nil || intent == nil || len(*deleted) != 1 || c.Reason != "DemotionRebooting" {
		t.Errorf("intent %+v (%v), deletes %q, condition %+v; want the intent, one delete, and the cleanup's condition", intent, err, *deleted, c)
	}
	if len(out.failures) != 0 || len(out.writes) != 1 {
		t.Errorf("failures %v and writes %q, want the intent as the one write", out.failures, out.writes)
	}
}

// A reboot intent that cannot be written leaves the Node in place,
// because deleting it would kill this pod with no reboot to follow.
func TestADemotionWhoseIntentFailsKeepsTheNode(t *testing.T) {
	node, deleted, handler := demotingNode(t, http.StatusOK)
	runDir := filepath.Join(t.TempDir(), "missing")
	out := &passOutcome{}

	c := carryOutDemotion(testClient(t, handler), runDir, node, demotion{cleanup: true}, out)

	if c.Reason != "DemotionFailed" || len(*deleted) != 0 || len(out.failures) != 1 {
		t.Errorf("condition %+v, deletes %q, failures %v; want DemotionFailed, no delete, and one failure", c, *deleted, out.failures)
	}
}

// A delete that fails after the intent landed leaves a failure for the
// pass to retry, and the reboot goes ahead.
func TestADemotionWhoseDeleteFailsIsRetried(t *testing.T) {
	node, _, handler := demotingNode(t, http.StatusServiceUnavailable)
	runDir := t.TempDir()
	out := &passOutcome{}

	carryOutDemotion(testClient(t, handler).WithObserver(out.observe), runDir, node, demotion{cleanup: true}, out)

	intent, _ := machine.ReadRebootIntent(runDir)
	if intent == nil || !slices.Equal(failureKinds(out), []failureKind{transient}) {
		t.Errorf("intent %+v and failure kinds %v, want the intent and one transient failure", intent, failureKinds(out))
	}
}
