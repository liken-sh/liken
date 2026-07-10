package main

// Tests for node-label reconciliation. The decision is a pure
// function over the spec and the Node, so every case here pins the
// patch and the condition without a cluster; the one I/O path
// (applying the patch) is exercised against a test server.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/liken-sh/liken/machine"
)

func nodeWearing(labels, annotations map[string]string) *nodeObject {
	n := &nodeObject{}
	n.Metadata.Name = "node-1"
	n.Metadata.Labels = labels
	n.Metadata.Annotations = annotations
	return n
}

// decodeLabelPatch unpacks a merge patch's metadata so assertions can
// see exactly which labels are set, which are erased (present but
// null), and what happened to the ownership annotation.
func decodeLabelPatch(t *testing.T, patch []byte) (labels, annotations map[string]any) {
	t.Helper()
	var doc struct {
		Metadata struct {
			Labels      map[string]any `json:"labels"`
			Annotations map[string]any `json:"annotations"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(patch, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Metadata.Labels, doc.Metadata.Annotations
}

func TestNodeLabelsNothingDeclaredIsQuiet(t *testing.T) {
	step := decideNodeLabels(nil, nodeWearing(map[string]string{"kubernetes.io/hostname": "node-1"}, nil))
	if step.patch != nil {
		t.Errorf("nothing declared and nothing owned should patch nothing: %s", step.patch)
	}
	if step.condition.Status != machine.ConditionTrue || step.condition.Reason != "NothingDeclared" {
		t.Errorf("condition: %+v", step.condition)
	}
}

func TestNodeLabelsFirstApplication(t *testing.T) {
	desired := map[string]string{"guid.foo/gpu": "true", "topology.kubernetes.io/zone": "closet"}
	step := decideNodeLabels(desired, nodeWearing(nil, nil))
	labels, annotations := decodeLabelPatch(t, step.patch)
	if labels["guid.foo/gpu"] != "true" || labels["topology.kubernetes.io/zone"] != "closet" {
		t.Errorf("labels: %v", labels)
	}
	if annotations[ownedLabelsAnnotation] != "guid.foo/gpu,topology.kubernetes.io/zone" {
		t.Errorf("ownership annotation should record the managed keys sorted: %v", annotations)
	}
	if step.condition.Status != machine.ConditionTrue || step.condition.Reason != "Applied" {
		t.Errorf("condition: %+v", step.condition)
	}
}

func TestNodeLabelsSettledNodeNeedsNoPatch(t *testing.T) {
	desired := map[string]string{"guid.foo/gpu": "true"}
	node := nodeWearing(
		map[string]string{"guid.foo/gpu": "true", "kubernetes.io/hostname": "node-1"},
		map[string]string{ownedLabelsAnnotation: "guid.foo/gpu"})
	step := decideNodeLabels(desired, node)
	if step.patch != nil {
		t.Errorf("a settled node should not be patched: %s", step.patch)
	}
	if step.condition.Status != machine.ConditionTrue || step.condition.Reason != "Applied" {
		t.Errorf("condition: %+v", step.condition)
	}
}

func TestNodeLabelsDriftIsReasserted(t *testing.T) {
	// Someone rewrote the label's value out from under the spec; the
	// next pass puts it back, the same way sysctls re-assert.
	desired := map[string]string{"guid.foo/gpu": "true"}
	node := nodeWearing(
		map[string]string{"guid.foo/gpu": "false"},
		map[string]string{ownedLabelsAnnotation: "guid.foo/gpu"})
	step := decideNodeLabels(desired, node)
	labels, _ := decodeLabelPatch(t, step.patch)
	if labels["guid.foo/gpu"] != "true" {
		t.Errorf("drifted label should be re-asserted: %v", labels)
	}
}

func TestNodeLabelsRetractedLabelIsRemoved(t *testing.T) {
	// The spec no longer declares the label, and the ownership
	// annotation proves it was liken's: merge-patch null erases it,
	// and the emptied annotation goes with it.
	node := nodeWearing(
		map[string]string{"guid.foo/gpu": "true"},
		map[string]string{ownedLabelsAnnotation: "guid.foo/gpu"})
	step := decideNodeLabels(nil, node)
	labels, annotations := decodeLabelPatch(t, step.patch)
	if value, present := labels["guid.foo/gpu"]; !present || value != nil {
		t.Errorf("retracted label should be erased with null: %v", labels)
	}
	if value, present := annotations[ownedLabelsAnnotation]; !present || value != nil {
		t.Errorf("an empty ownership annotation should be erased too: %v", annotations)
	}
	if step.condition.Reason != "NothingDeclared" {
		t.Errorf("condition: %+v", step.condition)
	}
}

func TestNodeLabelsRetractionKeepsTheRest(t *testing.T) {
	desired := map[string]string{"guid.foo/nas": "true"}
	node := nodeWearing(
		map[string]string{"guid.foo/gpu": "true", "guid.foo/nas": "true"},
		map[string]string{ownedLabelsAnnotation: "guid.foo/gpu,guid.foo/nas"})
	step := decideNodeLabels(desired, node)
	labels, annotations := decodeLabelPatch(t, step.patch)
	if value, present := labels["guid.foo/gpu"]; !present || value != nil {
		t.Errorf("the retracted label should be erased: %v", labels)
	}
	if _, present := labels["guid.foo/nas"]; present {
		t.Errorf("the still-declared label already holds; patching it is noise: %v", labels)
	}
	if annotations[ownedLabelsAnnotation] != "guid.foo/nas" {
		t.Errorf("ownership annotation should shrink to the declared keys: %v", annotations)
	}
}

func TestNodeLabelsNeverTouchForeignLabels(t *testing.T) {
	// A label someone applied by hand is not in the ownership
	// annotation, so retracting nothing removes nothing: the operator
	// only ever removes what it can prove it added.
	node := nodeWearing(map[string]string{"team": "storage"}, nil)
	step := decideNodeLabels(nil, node)
	if step.patch != nil {
		t.Errorf("a hand-applied label is not liken's to remove: %s", step.patch)
	}
}

func TestCarryOutNodeLabelsAppliesThePatch(t *testing.T) {
	var patched []byte
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && r.URL.Path == "/api/v1/nodes/node-1" {
			patched = make([]byte, r.ContentLength)
			_, _ = r.Body.Read(patched)
		}
		w.WriteHeader(http.StatusOK)
	}))
	step := decideNodeLabels(map[string]string{"guid.foo/gpu": "true"}, nodeWearing(nil, nil))
	condition := carryOutNodeLabels(c, "node-1", step)
	if condition.Status != machine.ConditionTrue || condition.Reason != "Applied" {
		t.Errorf("condition: %+v", condition)
	}
	if len(patched) == 0 {
		t.Error("the patch should have been sent to the Node")
	}
}

func TestCarryOutNodeLabelsReportsAFailedPatch(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	step := decideNodeLabels(map[string]string{"guid.foo/gpu": "true"}, nodeWearing(nil, nil))
	condition := carryOutNodeLabels(c, "node-1", step)
	if condition.Status != machine.ConditionFalse || condition.Reason != "ApplyFailed" {
		t.Errorf("a failed patch should report ApplyFailed: %+v", condition)
	}
}
