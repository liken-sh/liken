package main

// A follower whose Node still has control-plane labels was demoted, and
// its Node must be deleted so that k3s removes its etcd member.

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
		t.Error("the decision has no cleanup, want cleanup for a follower with a control-plane Node")
	}
	if d.condition.Status != "False" || d.condition.Reason != "DemotionRebooting" {
		t.Errorf("the condition is %+v, want False with reason DemotionRebooting", d.condition)
	}
}

func TestDemotionCleanupWaitsUnderManualPolicy(t *testing.T) {
	labels := map[string]string{"node-role.kubernetes.io/etcd": "true"}
	d := decideDemotion(api.RoleFollower, labels, machine.RebootManual, turnGranted)
	if d.cleanup {
		t.Error("the decision has a cleanup, want none because the cleanup deletes the Node and reboots, and the Manual policy forbids that")
	}
	if d.condition.Status != "False" || d.condition.Reason != "DemotionPending" {
		t.Errorf("the condition is %+v, want False with reason DemotionPending", d.condition)
	}
}

func TestACleanFollowerNeedsNothing(t *testing.T) {
	d := decideDemotion(api.RoleFollower, map[string]string{"kubernetes.io/hostname": "node-4"}, machine.RebootAuto, turnGranted)
	if d.cleanup || d.condition.Status != "True" {
		t.Errorf("the decision is %+v, want no cleanup and a True condition", d)
	}
}

func TestALeaderIsAlwaysCurrent(t *testing.T) {
	// A leader with control-plane labels is correct. One without them
	// is still starting, and k3s adds them.
	labels := map[string]string{"node-role.kubernetes.io/control-plane": "true"}
	d := decideDemotion(api.RoleLeader, labels, machine.RebootAuto, turnGranted)
	if d.cleanup || d.condition.Status != "True" {
		t.Errorf("the decision is %+v, want no cleanup and a True condition", d)
	}
}

func TestDemotionWaitsForItsRebootTurn(t *testing.T) {
	labels := map[string]string{"node-role.kubernetes.io/control-plane": "true"}
	d := decideDemotion(api.RoleFollower, labels, machine.RebootAuto, turnAwaiting)
	if d.cleanup {
		t.Error("the decision has a cleanup, want none because no reboot turn is granted")
	}
	if d.condition.Reason != "AwaitingTurn" {
		t.Errorf("the condition is %+v, want reason AwaitingTurn", d.condition)
	}
}

// The delete sends the UID of the Node the pass read as a precondition.
// If k3s registered a new Node under the same name after that read, the
// API server returns 409 and deletes nothing.
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
		t.Fatalf("deleting the Node: %v", err)
	}

	if method != http.MethodDelete || path != "/api/v1/nodes/node-2" || sent.Kind != "DeleteOptions" || sent.Preconditions.UID != "uid-read" {
		t.Errorf("sent %s %s with %+v, want a DELETE of node-2 whose precondition is uid-read", method, path, sent)
	}
}

// demotingNode returns the stale control-plane Node that a demotion
// removes, and a handler that records each DELETE path and responds
// with status.
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

func TestTheDemotionWritesTheRebootIntentAndDeletesTheNode(t *testing.T) {
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
// because deleting it would delete this pod with no reboot to follow.
func TestADemotionWhoseIntentFailsKeepsTheNode(t *testing.T) {
	node, deleted, handler := demotingNode(t, http.StatusOK)
	runDir := filepath.Join(t.TempDir(), "missing")
	out := &passOutcome{}

	c := carryOutDemotion(testClient(t, handler), runDir, node, demotion{cleanup: true}, out)

	if c.Reason != "DemotionFailed" || len(*deleted) != 0 || len(out.failures) != 1 {
		t.Errorf("condition %+v, deletes %q, failures %v; want DemotionFailed, no delete, and one failure", c, *deleted, out.failures)
	}
}

// A delete that fails after the intent was written is recorded as a
// transient failure for the pass to retry, and the reboot still
// happens.
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
