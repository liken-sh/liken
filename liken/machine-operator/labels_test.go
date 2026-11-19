package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/liken-sh/liken/liken/api"
)

func nodeWithLabels(labels, annotations map[string]string) *nodeObject {
	n := &nodeObject{}
	n.Metadata.Name = "node-1"
	n.Metadata.Labels = labels
	n.Metadata.Annotations = annotations
	return n
}

// decodeLabelPatch returns the labels and annotations of a merge
// patch. A key with a null value is one that the patch deletes.
func decodeLabelPatch(t *testing.T, patch []byte) (labels, annotations map[string]any) {
	t.Helper()
	var doc struct {
		Metadata struct {
			Labels      map[string]any `json:"labels"`
			Annotations map[string]any `json:"annotations"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(patch, &doc); err != nil {
		t.Fatalf("decoding the label patch: %v", err)
	}
	return doc.Metadata.Labels, doc.Metadata.Annotations
}

func TestNodeLabelsNothingDeclaredPatchesNothing(t *testing.T) {
	step := decideNodeLabels(nil, nodeWithLabels(map[string]string{"kubernetes.io/hostname": "node-1"}, nil))
	if step.patch != nil {
		t.Errorf("patch = %s, want none because no labels are declared and none are owned", step.patch)
	}
	if step.condition.Status != api.ConditionTrue || step.condition.Reason != "NothingDeclared" {
		t.Errorf("condition = %+v, want True NothingDeclared", step.condition)
	}
}

func TestNodeLabelsFirstApplication(t *testing.T) {
	desired := map[string]string{"guid.foo/gpu": "true", "topology.kubernetes.io/zone": "closet"}
	step := decideNodeLabels(desired, nodeWithLabels(nil, nil))
	labels, annotations := decodeLabelPatch(t, step.patch)
	if labels["guid.foo/gpu"] != "true" || labels["topology.kubernetes.io/zone"] != "closet" {
		t.Errorf("labels = %v, want both declared labels", labels)
	}
	if annotations[ownedLabelsAnnotation] != "guid.foo/gpu,topology.kubernetes.io/zone" {
		t.Errorf("ownership annotation = %v, want the declared keys in sorted order", annotations)
	}
	if step.condition.Status != api.ConditionTrue || step.condition.Reason != "Applied" {
		t.Errorf("condition = %+v, want True Applied", step.condition)
	}
}

func TestNodeLabelsMatchingNodeNeedsNoPatch(t *testing.T) {
	desired := map[string]string{"guid.foo/gpu": "true"}
	node := nodeWithLabels(
		map[string]string{"guid.foo/gpu": "true", "kubernetes.io/hostname": "node-1"},
		map[string]string{ownedLabelsAnnotation: "guid.foo/gpu"})
	step := decideNodeLabels(desired, node)
	if step.patch != nil {
		t.Errorf("patch = %s, want none because the node matches the spec", step.patch)
	}
	if step.condition.Status != api.ConditionTrue || step.condition.Reason != "Applied" {
		t.Errorf("condition = %+v, want True Applied", step.condition)
	}
}

func TestNodeLabelsChangedValueIsReset(t *testing.T) {
	desired := map[string]string{"guid.foo/gpu": "true"}
	node := nodeWithLabels(
		map[string]string{"guid.foo/gpu": "false"},
		map[string]string{ownedLabelsAnnotation: "guid.foo/gpu"})
	step := decideNodeLabels(desired, node)
	labels, _ := decodeLabelPatch(t, step.patch)
	if labels["guid.foo/gpu"] != "true" {
		t.Errorf("labels = %v, want the label with the changed value set again", labels)
	}
}

func TestNodeLabelsLabelRemovedFromSpecIsDeleted(t *testing.T) {
	// The annotation lists the key, so the patch deletes the label, and
	// then the annotation, which would be empty.
	node := nodeWithLabels(
		map[string]string{"guid.foo/gpu": "true"},
		map[string]string{ownedLabelsAnnotation: "guid.foo/gpu"})
	step := decideNodeLabels(nil, node)
	labels, annotations := decodeLabelPatch(t, step.patch)
	if value, present := labels["guid.foo/gpu"]; !present || value != nil {
		t.Errorf("labels = %v, want the label removed from the spec deleted with null", labels)
	}
	if value, present := annotations[ownedLabelsAnnotation]; !present || value != nil {
		t.Errorf("annotations = %v, want the empty ownership annotation deleted too", annotations)
	}
	if step.condition.Reason != "NothingDeclared" {
		t.Errorf("condition = %+v, want reason NothingDeclared after the last declared label is removed", step.condition)
	}
}

func TestNodeLabelsRemovalKeepsTheOtherLabels(t *testing.T) {
	desired := map[string]string{"guid.foo/nas": "true"}
	node := nodeWithLabels(
		map[string]string{"guid.foo/gpu": "true", "guid.foo/nas": "true"},
		map[string]string{ownedLabelsAnnotation: "guid.foo/gpu,guid.foo/nas"})
	step := decideNodeLabels(desired, node)
	labels, annotations := decodeLabelPatch(t, step.patch)
	if value, present := labels["guid.foo/gpu"]; !present || value != nil {
		t.Errorf("labels = %v, want the label removed from the spec deleted", labels)
	}
	if _, present := labels["guid.foo/nas"]; present {
		t.Errorf("labels = %v, want no label that already matches the spec", labels)
	}
	if annotations[ownedLabelsAnnotation] != "guid.foo/nas" {
		t.Errorf("annotations = %v, want only the declared keys in the ownership annotation", annotations)
	}
}

func TestNodeLabelsLeaveOtherLabelsAlone(t *testing.T) {
	// The annotation does not list a label that a person set.
	node := nodeWithLabels(map[string]string{"team": "storage"}, nil)
	step := decideNodeLabels(nil, node)
	if step.patch != nil {
		t.Errorf("patch = %s, want none because a label that a person set is not this operator's to remove", step.patch)
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
	step := decideNodeLabels(map[string]string{"guid.foo/gpu": "true"}, nodeWithLabels(nil, nil))
	condition := carryOutNodeLabels(c, "node-1", step)
	if condition.Status != api.ConditionTrue || condition.Reason != "Applied" {
		t.Errorf("condition = %+v, want True Applied", condition)
	}
	if len(patched) == 0 {
		t.Error("no patch was sent to the Node, want one")
	}
}

func TestCarryOutNodeLabelsReportsAFailedPatch(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	step := decideNodeLabels(map[string]string{"guid.foo/gpu": "true"}, nodeWithLabels(nil, nil))
	condition := carryOutNodeLabels(c, "node-1", step)
	if condition.Status != api.ConditionFalse || condition.Reason != "ApplyFailed" {
		t.Errorf("condition = %+v, want False ApplyFailed because the API server refused the patch", condition)
	}
}

// The label that keeps an operator's DaemonSet off a node, such as
// equipment.liken.sh/cec: none, is an ordinary key. When a person set
// it with kubectl before the spec declared it, the annotation still
// lists it, so the operator can remove it later.
func TestNodeLabelsOperatorNoneLabel(t *testing.T) {
	const key = "equipment.liken.sh/cec"
	cases := []struct {
		name             string
		desired          map[string]string
		node             *nodeObject
		wantLabel        any
		wantLabelInPatch bool
		wantOwned        any
	}{
		{"applied", map[string]string{key: "none"}, nodeWithLabels(nil, nil), "none", true, key},
		{"already set with kubectl", map[string]string{key: "none"},
			nodeWithLabels(map[string]string{key: "none"}, nil), nil, false, key},
		{"removed", nil,
			nodeWithLabels(map[string]string{key: "none"}, map[string]string{ownedLabelsAnnotation: key}), nil, true, nil},
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
