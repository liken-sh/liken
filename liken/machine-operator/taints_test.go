package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// notReady is a taint that the node lifecycle controller owns. A patch
// writes the full taint list, so most cases put notReady on the Node and
// check that the patch keeps it.
var notReady = nodeTaint{Key: "node.kubernetes.io/not-ready", Effect: "NoExecute"}

func nodeWithTaints(taints []nodeTaint, annotations map[string]string) *nodeObject {
	n := &nodeObject{}
	n.Metadata.Name = "node-1"
	n.Metadata.ResourceVersion = "4242"
	n.Metadata.Annotations = annotations
	n.Spec.Taints = taints
	return n
}

// decodeTaintPatch decodes a merge patch, so assertions can read the
// taint list that it writes, the change to the ownership annotation,
// and whether it sets a resourceVersion precondition.
func decodeTaintPatch(t *testing.T, patch []byte) (taints []nodeTaint, annotations map[string]any, version string) {
	t.Helper()
	var doc struct {
		Metadata struct {
			Annotations     map[string]any `json:"annotations"`
			ResourceVersion string         `json:"resourceVersion"`
		} `json:"metadata"`
		Spec struct {
			Taints []nodeTaint `json:"taints"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(patch, &doc); err != nil {
		t.Fatalf("decoding the patch: %v", err)
	}
	return doc.Spec.Taints, doc.Metadata.Annotations, doc.Metadata.ResourceVersion
}

func drillTaint(value string) machine.NodeTaint {
	return machine.NodeTaint{Key: "guid.foo/drill", Value: value, Effect: machine.TaintPreferNoSchedule}
}

func TestNodeTaintsNothingDeclaredIsQuiet(t *testing.T) {
	step := decideNodeTaints(nil, nodeWithTaints([]nodeTaint{notReady}, nil))
	if step.patch != nil {
		t.Errorf("the patch is %s, want none when nothing is declared or owned", step.patch)
	}
	if step.condition.Status != api.ConditionTrue || step.condition.Reason != "NothingDeclared" {
		t.Errorf("the condition is %+v, want True with reason NothingDeclared", step.condition)
	}
}

func TestNodeTaintsFirstApplication(t *testing.T) {
	desired := []machine.NodeTaint{
		drillTaint("node-taints"),
		{Key: "guid.foo/encode", Value: "only", Effect: machine.TaintNoSchedule},
	}
	step := decideNodeTaints(desired, nodeWithTaints([]nodeTaint{notReady}, nil))
	taints, annotations, version := decodeTaintPatch(t, step.patch)
	want := []nodeTaint{
		notReady,
		{Key: "guid.foo/drill", Value: "node-taints", Effect: "PreferNoSchedule"},
		{Key: "guid.foo/encode", Value: "only", Effect: "NoSchedule"},
	}
	if len(taints) != len(want) {
		t.Fatalf("the patch writes taints %v, want the full list with the Node's other taints", taints)
	}
	for i := range want {
		if taints[i] != want[i] {
			t.Errorf("taint %d is %v, want %v", i, taints[i], want[i])
		}
	}
	if annotations[ownedTaintsAnnotation] != "guid.foo/drill:PreferNoSchedule,guid.foo/encode:NoSchedule" {
		t.Errorf("the annotations are %v, want the sorted pairs the operator applied", annotations)
	}
	if version != "4242" {
		t.Errorf("the patch resourceVersion is %q, want 4242, the version that the pass read, because the patch writes the full list", version)
	}
	if step.condition.Status != api.ConditionTrue || step.condition.Reason != "Applied" {
		t.Errorf("the condition is %+v, want True with reason Applied", step.condition)
	}
}

func TestNodeTaintsSettledNodeNeedsNoPatch(t *testing.T) {
	node := nodeWithTaints(
		[]nodeTaint{notReady, {Key: "guid.foo/drill", Value: "node-taints", Effect: "PreferNoSchedule"}},
		map[string]string{ownedTaintsAnnotation: "guid.foo/drill:PreferNoSchedule"})
	step := decideNodeTaints([]machine.NodeTaint{drillTaint("node-taints")}, node)
	if step.patch != nil {
		t.Errorf("the patch is %s, want none because the Node already matches", step.patch)
	}
	if step.condition.Status != api.ConditionTrue || step.condition.Reason != "Applied" {
		t.Errorf("the condition is %+v, want True with reason Applied", step.condition)
	}
}

func TestNodeTaintsValueDriftIsReasserted(t *testing.T) {
	// Someone changed the value of a taint that the operator owns. The
	// key and effect still match, so the pass rewrites that entry and
	// does not add a second one.
	node := nodeWithTaints(
		[]nodeTaint{notReady, {Key: "guid.foo/drill", Value: "stale", Effect: "PreferNoSchedule"}},
		map[string]string{ownedTaintsAnnotation: "guid.foo/drill:PreferNoSchedule"})
	step := decideNodeTaints([]machine.NodeTaint{drillTaint("node-taints")}, node)
	taints, _, version := decodeTaintPatch(t, step.patch)
	want := []nodeTaint{notReady, {Key: "guid.foo/drill", Value: "node-taints", Effect: "PreferNoSchedule"}}
	if len(taints) != 2 || taints[0] != want[0] || taints[1] != want[1] {
		t.Errorf("the taints are %v, want the drifted value rewritten in place", taints)
	}
	if version != "4242" {
		t.Errorf("the patch resourceVersion is %q, want 4242", version)
	}
}

func TestNodeTaintsRetractedTaintIsRemoved(t *testing.T) {
	// The spec no longer declares a taint that the annotation lists.
	// The patch writes the list without it and sets the annotation,
	// now empty, to null.
	node := nodeWithTaints(
		[]nodeTaint{notReady, {Key: "guid.foo/drill", Value: "node-taints", Effect: "PreferNoSchedule"}},
		map[string]string{ownedTaintsAnnotation: "guid.foo/drill:PreferNoSchedule"})
	step := decideNodeTaints(nil, node)
	taints, annotations, version := decodeTaintPatch(t, step.patch)
	if len(taints) != 1 || taints[0] != notReady {
		t.Errorf("the taints are %v, want only the taint that another writer applied", taints)
	}
	if value, present := annotations[ownedTaintsAnnotation]; !present || value != nil {
		t.Errorf("the annotations are %v, want the ownership annotation set to null", annotations)
	}
	if version != "4242" {
		t.Errorf("the patch resourceVersion is %q, want 4242", version)
	}
	if step.condition.Reason != "NothingDeclared" {
		t.Errorf("the condition is %+v, want reason NothingDeclared", step.condition)
	}
}

func TestNodeTaintsRetractionKeepsTheRest(t *testing.T) {
	node := nodeWithTaints(
		[]nodeTaint{
			{Key: "guid.foo/drill", Value: "node-taints", Effect: "PreferNoSchedule"},
			notReady,
			{Key: "guid.foo/encode", Value: "only", Effect: "NoSchedule"},
		},
		map[string]string{ownedTaintsAnnotation: "guid.foo/drill:PreferNoSchedule,guid.foo/encode:NoSchedule"})
	step := decideNodeTaints([]machine.NodeTaint{drillTaint("node-taints")}, node)
	taints, annotations, _ := decodeTaintPatch(t, step.patch)
	want := []nodeTaint{{Key: "guid.foo/drill", Value: "node-taints", Effect: "PreferNoSchedule"}, notReady}
	if len(taints) != 2 || taints[0] != want[0] || taints[1] != want[1] {
		t.Errorf("the taints are %v, want the retracted taint removed and the others in their positions", taints)
	}
	if annotations[ownedTaintsAnnotation] != "guid.foo/drill:PreferNoSchedule" {
		t.Errorf("the annotations are %v, want only the declared pairs", annotations)
	}
}

func TestNodeTaintsNeverTouchAForeignTaint(t *testing.T) {
	// A taint applied by hand is in neither the spec nor the
	// annotation, so the operator leaves it.
	node := nodeWithTaints([]nodeTaint{{Key: "team", Value: "storage", Effect: "NoSchedule"}}, nil)
	step := decideNodeTaints(nil, node)
	if step.patch != nil {
		t.Errorf("the patch is %s, want none because the operator did not apply the taint", step.patch)
	}
}

func TestNodeTaintsSameKeyOtherEffectIsForeign(t *testing.T) {
	// A second effect under a declared key is a different taint, which
	// the operator did not apply.
	node := nodeWithTaints(
		[]nodeTaint{{Key: "guid.foo/drill", Value: "by-hand", Effect: "NoSchedule"}},
		map[string]string{ownedTaintsAnnotation: "guid.foo/drill:PreferNoSchedule"})
	step := decideNodeTaints([]machine.NodeTaint{drillTaint("node-taints")}, node)
	taints, _, _ := decodeTaintPatch(t, step.patch)
	want := []nodeTaint{
		{Key: "guid.foo/drill", Value: "by-hand", Effect: "NoSchedule"},
		{Key: "guid.foo/drill", Value: "node-taints", Effect: "PreferNoSchedule"},
	}
	if len(taints) != 2 || taints[0] != want[0] || taints[1] != want[1] {
		t.Errorf("the taints are %v, want both effects of the key", taints)
	}
}

func TestNodeTaintsValuelessTaintCarriesNoValue(t *testing.T) {
	desired := []machine.NodeTaint{{Key: "guid.foo/drill", Effect: machine.TaintNoSchedule}}
	step := decideNodeTaints(desired, nodeWithTaints([]nodeTaint{notReady}, nil))
	taints, _, _ := decodeTaintPatch(t, step.patch)
	if len(taints) != 2 || taints[1] != (nodeTaint{Key: "guid.foo/drill", Effect: "NoSchedule"}) {
		t.Errorf("the taints are %v, want the taint with no value", taints)
	}
	if strings.Contains(string(step.patch), `"value"`) {
		t.Errorf("the patch is %s, want no value field for an empty value", step.patch)
	}
}

func TestNodeTaintsValuelessTaintNeedsNoSecondPatch(t *testing.T) {
	node := nodeWithTaints(
		[]nodeTaint{notReady, {Key: "guid.foo/drill", Effect: "NoSchedule"}},
		map[string]string{ownedTaintsAnnotation: "guid.foo/drill:NoSchedule"})
	step := decideNodeTaints([]machine.NodeTaint{{Key: "guid.foo/drill", Effect: machine.TaintNoSchedule}}, node)
	if step.patch != nil {
		t.Errorf("the patch is %s, want none because the Node already has the taint", step.patch)
	}
}

func TestNodeTaintsAdoptionWritesOnlyTheAnnotation(t *testing.T) {
	// The declared taint is already on the Node and only the annotation
	// is missing, so the patch writes no taint list and needs no
	// resourceVersion precondition.
	node := nodeWithTaints(
		[]nodeTaint{notReady, {Key: "guid.foo/drill", Value: "node-taints", Effect: "PreferNoSchedule"}}, nil)
	step := decideNodeTaints([]machine.NodeTaint{drillTaint("node-taints")}, node)
	taints, annotations, version := decodeTaintPatch(t, step.patch)
	if taints != nil {
		t.Errorf("the patch writes taints %v, want none because the list already matches", taints)
	}
	if annotations[ownedTaintsAnnotation] != "guid.foo/drill:PreferNoSchedule" {
		t.Errorf("the annotations are %v, want the ownership annotation for the declared taint", annotations)
	}
	if version != "" {
		t.Errorf("the patch resourceVersion is %q, want none because the patch writes no taint list", version)
	}
}

func TestCarryOutNodeTaintsAppliesThePatch(t *testing.T) {
	var patched []byte
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && r.URL.Path == "/api/v1/nodes/node-1" {
			patched = make([]byte, r.ContentLength)
			_, _ = r.Body.Read(patched)
		}
		w.WriteHeader(http.StatusOK)
	}))
	step := decideNodeTaints([]machine.NodeTaint{drillTaint("node-taints")}, nodeWithTaints(nil, nil))
	condition := carryOutNodeTaints(c, "node-1", step)
	if condition.Status != api.ConditionTrue || condition.Reason != "Applied" {
		t.Errorf("the condition is %+v, want True with reason Applied", condition)
	}
	if len(patched) == 0 {
		t.Error("no patch reached the Node, want one")
	}
}

func TestCarryOutNodeTaintsReportsAFailedPatch(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	step := decideNodeTaints([]machine.NodeTaint{drillTaint("node-taints")}, nodeWithTaints(nil, nil))
	condition := carryOutNodeTaints(c, "node-1", step)
	if condition.Status != api.ConditionFalse || condition.Reason != "ApplyFailed" {
		t.Errorf("the condition is %+v, want False with reason ApplyFailed after the API server refused the patch", condition)
	}
}
