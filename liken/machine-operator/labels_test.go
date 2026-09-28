package main

// Tests for node-label reconciliation. The decision is a pure
// function over the spec and the Node, so every case here checks
// the patch and the condition without a cluster. The one I/O path,
// applying the patch, is tested against a test server.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/liken-sh/liken/liken/api"
)

func nodeWearing(labels, annotations map[string]string) *nodeObject {
	n := &nodeObject{}
	n.Metadata.Name = "node-1"
	n.Metadata.Labels = labels
	n.Metadata.Annotations = annotations
	return n
}

// decodeLabelPatch unpacks a merge patch's metadata, so assertions
// can see exactly which labels are set, which are erased (present
// but null), and what happened to the ownership annotation.
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
	if step.condition.Status != api.ConditionTrue || step.condition.Reason != "NothingDeclared" {
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
	if step.condition.Status != api.ConditionTrue || step.condition.Reason != "Applied" {
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
	if step.condition.Status != api.ConditionTrue || step.condition.Reason != "Applied" {
		t.Errorf("condition: %+v", step.condition)
	}
}

func TestNodeLabelsDriftIsReasserted(t *testing.T) {
	// Something else changed the label's value away from the spec.
	// The next pass puts it back, the same way sysctls get
	// reapplied.
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
	// annotation proves it belonged to liken. A null value in the
	// merge patch erases it, and the emptied annotation goes with
	// it.
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
	// A label that someone applied by hand is not in the ownership
	// annotation, so retracting nothing removes nothing. The
	// operator only ever removes what it can prove it added.
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
	if condition.Status != api.ConditionTrue || condition.Reason != "Applied" {
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
	if condition.Status != api.ConditionFalse || condition.Reason != "ApplyFailed" {
		t.Errorf("a failed patch should report ApplyFailed: %+v", condition)
	}
}

// An operator's DaemonSet stays off a node labeled
// <group>/<hardware>: none, in the operator's own subdomain of
// liken.sh. The operator applies and removes that label the same way
// as any other key. A label that kubectl set before the Machine
// declared it becomes the operator's to remove, because the
// ownership annotation records the key once the spec declares it.
func TestNodeLabelsAnOperatorsNoneLabel(t *testing.T) {
	const key = "equipment.liken.sh/cec"
	cases := []struct {
		name             string
		desired          map[string]string
		node             *nodeObject
		wantLabel        any
		wantLabelInPatch bool
		wantOwned        any
	}{
		{"applied", map[string]string{key: "none"}, nodeWearing(nil, nil), "none", true, key},
		{"adopted from kubectl", map[string]string{key: "none"},
			nodeWearing(map[string]string{key: "none"}, nil), nil, false, key},
		{"removed", nil,
			nodeWearing(map[string]string{key: "none"}, map[string]string{ownedLabelsAnnotation: key}), nil, true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			labels, annotations := decodeLabelPatch(t, decideNodeLabels(tc.desired, tc.node).patch)
			value, present := labels[key]
			if present != tc.wantLabelInPatch || value != tc.wantLabel {
				t.Errorf("label in patch: %v (present %v), want %v (present %v)", value, present, tc.wantLabel, tc.wantLabelInPatch)
			}
			if owned, present := annotations[ownedLabelsAnnotation]; !present || owned != tc.wantOwned {
				t.Errorf("ownership annotation: %v (present %v), want %v", owned, present, tc.wantOwned)
			}
		})
	}
}
