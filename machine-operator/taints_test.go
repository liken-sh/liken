package main

// Tests for node-taint reconciliation. The decision is a pure
// function over the spec and the Node, so every case here checks the
// patch and the condition without a cluster. The one I/O path,
// applying the patch, is tested against a test server.
//
// notReady stands in for the node lifecycle controller's own taint in
// every case. It is the taint the operator is most likely to meet on
// a real Node, and the one a full-list write would erase, so every
// patching case asserts that it survives.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
)

var notReady = nodeTaint{Key: "node.kubernetes.io/not-ready", Effect: "NoExecute"}

func nodeRepelling(taints []nodeTaint, annotations map[string]string) *nodeObject {
	n := &nodeObject{}
	n.Metadata.Name = "node-1"
	n.Metadata.ResourceVersion = "4242"
	n.Metadata.Annotations = annotations
	n.Spec.Taints = taints
	return n
}

// decodeTaintPatch unpacks a merge patch, so assertions can see the
// full taint list the patch writes, what happened to the ownership
// annotation, and whether the write carries its resourceVersion
// precondition.
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
		t.Fatal(err)
	}
	return doc.Spec.Taints, doc.Metadata.Annotations, doc.Metadata.ResourceVersion
}

func drillTaint(value string) machine.NodeTaint {
	return machine.NodeTaint{Key: "guid.foo/drill", Value: value, Effect: machine.TaintPreferNoSchedule}
}

func TestNodeTaintsNothingDeclaredIsQuiet(t *testing.T) {
	step := decideNodeTaints(nil, nodeRepelling([]nodeTaint{notReady}, nil))
	if step.patch != nil {
		t.Errorf("nothing declared and nothing owned should patch nothing: %s", step.patch)
	}
	if step.condition.Status != api.ConditionTrue || step.condition.Reason != "NothingDeclared" {
		t.Errorf("condition: %+v", step.condition)
	}
}

func TestNodeTaintsFirstApplication(t *testing.T) {
	desired := []machine.NodeTaint{
		drillTaint("node-taints"),
		{Key: "guid.foo/encode", Value: "only", Effect: machine.TaintNoSchedule},
	}
	step := decideNodeTaints(desired, nodeRepelling([]nodeTaint{notReady}, nil))
	taints, annotations, version := decodeTaintPatch(t, step.patch)
	want := []nodeTaint{
		notReady,
		{Key: "guid.foo/drill", Value: "node-taints", Effect: "PreferNoSchedule"},
		{Key: "guid.foo/encode", Value: "only", Effect: "NoSchedule"},
	}
	if len(taints) != len(want) {
		t.Fatalf("the patch must write the full list, foreign taints included: %v", taints)
	}
	for i := range want {
		if taints[i] != want[i] {
			t.Errorf("taint %d: %v, want %v", i, taints[i], want[i])
		}
	}
	if annotations[ownedTaintsAnnotation] != "guid.foo/drill:PreferNoSchedule,guid.foo/encode:NoSchedule" {
		t.Errorf("ownership annotation should record the managed pairs sorted: %v", annotations)
	}
	if version != "4242" {
		t.Errorf("a full-list write must name the resourceVersion it read: %q", version)
	}
	if step.condition.Status != api.ConditionTrue || step.condition.Reason != "Applied" {
		t.Errorf("condition: %+v", step.condition)
	}
}

func TestNodeTaintsSettledNodeNeedsNoPatch(t *testing.T) {
	node := nodeRepelling(
		[]nodeTaint{notReady, {Key: "guid.foo/drill", Value: "node-taints", Effect: "PreferNoSchedule"}},
		map[string]string{ownedTaintsAnnotation: "guid.foo/drill:PreferNoSchedule"})
	step := decideNodeTaints([]machine.NodeTaint{drillTaint("node-taints")}, node)
	if step.patch != nil {
		t.Errorf("a settled node should not be patched: %s", step.patch)
	}
	if step.condition.Status != api.ConditionTrue || step.condition.Reason != "Applied" {
		t.Errorf("condition: %+v", step.condition)
	}
}

func TestNodeTaintsValueDriftIsReasserted(t *testing.T) {
	// Something changed the value under a pair liken owns. The pair
	// still identifies the taint, so the next pass rewrites the entry
	// in place instead of adding a second one.
	node := nodeRepelling(
		[]nodeTaint{notReady, {Key: "guid.foo/drill", Value: "stale", Effect: "PreferNoSchedule"}},
		map[string]string{ownedTaintsAnnotation: "guid.foo/drill:PreferNoSchedule"})
	step := decideNodeTaints([]machine.NodeTaint{drillTaint("node-taints")}, node)
	taints, _, version := decodeTaintPatch(t, step.patch)
	want := []nodeTaint{notReady, {Key: "guid.foo/drill", Value: "node-taints", Effect: "PreferNoSchedule"}}
	if len(taints) != 2 || taints[0] != want[0] || taints[1] != want[1] {
		t.Errorf("the drifted value should be rewritten in place: %v", taints)
	}
	if version != "4242" {
		t.Errorf("resourceVersion: %q", version)
	}
}

func TestNodeTaintsRetractedTaintIsRemoved(t *testing.T) {
	// The spec no longer declares the taint, and the ownership
	// annotation proves it belonged to liken. The full list goes back
	// without it, and the emptied annotation is erased with a null.
	node := nodeRepelling(
		[]nodeTaint{notReady, {Key: "guid.foo/drill", Value: "node-taints", Effect: "PreferNoSchedule"}},
		map[string]string{ownedTaintsAnnotation: "guid.foo/drill:PreferNoSchedule"})
	step := decideNodeTaints(nil, node)
	taints, annotations, version := decodeTaintPatch(t, step.patch)
	if len(taints) != 1 || taints[0] != notReady {
		t.Errorf("the retracted taint should go and the foreign one stay: %v", taints)
	}
	if value, present := annotations[ownedTaintsAnnotation]; !present || value != nil {
		t.Errorf("an empty ownership annotation should be erased too: %v", annotations)
	}
	if version != "4242" {
		t.Errorf("resourceVersion: %q", version)
	}
	if step.condition.Reason != "NothingDeclared" {
		t.Errorf("condition: %+v", step.condition)
	}
}

func TestNodeTaintsRetractionKeepsTheRest(t *testing.T) {
	node := nodeRepelling(
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
		t.Errorf("only the retracted pair should go, and positions should hold: %v", taints)
	}
	if annotations[ownedTaintsAnnotation] != "guid.foo/drill:PreferNoSchedule" {
		t.Errorf("ownership annotation should shrink to the declared pairs: %v", annotations)
	}
}

func TestNodeTaintsNeverTouchAForeignTaint(t *testing.T) {
	// A taint applied by hand is in neither the spec nor the
	// annotation, so retracting nothing removes nothing. The operator
	// only ever removes what it can prove it applied.
	node := nodeRepelling([]nodeTaint{{Key: "team", Value: "storage", Effect: "NoSchedule"}}, nil)
	step := decideNodeTaints(nil, node)
	if step.patch != nil {
		t.Errorf("a hand-applied taint is not liken's to remove: %s", step.patch)
	}
}

func TestNodeTaintsSameKeyOtherEffectIsForeign(t *testing.T) {
	// Ownership runs on the pair, so a second effect under a declared
	// key is a different taint, and one that liken never applied.
	node := nodeRepelling(
		[]nodeTaint{{Key: "guid.foo/drill", Value: "by-hand", Effect: "NoSchedule"}},
		map[string]string{ownedTaintsAnnotation: "guid.foo/drill:PreferNoSchedule"})
	step := decideNodeTaints([]machine.NodeTaint{drillTaint("node-taints")}, node)
	taints, _, _ := decodeTaintPatch(t, step.patch)
	want := []nodeTaint{
		{Key: "guid.foo/drill", Value: "by-hand", Effect: "NoSchedule"},
		{Key: "guid.foo/drill", Value: "node-taints", Effect: "PreferNoSchedule"},
	}
	if len(taints) != 2 || taints[0] != want[0] || taints[1] != want[1] {
		t.Errorf("both effects should stand: %v", taints)
	}
}

func TestNodeTaintsValuelessTaintCarriesNoValue(t *testing.T) {
	desired := []machine.NodeTaint{{Key: "guid.foo/drill", Effect: machine.TaintNoSchedule}}
	step := decideNodeTaints(desired, nodeRepelling([]nodeTaint{notReady}, nil))
	taints, _, _ := decodeTaintPatch(t, step.patch)
	if len(taints) != 2 || taints[1] != (nodeTaint{Key: "guid.foo/drill", Effect: "NoSchedule"}) {
		t.Errorf("a value-less taint should reach the Node with no value: %v", taints)
	}
	if strings.Contains(string(step.patch), `"value"`) {
		t.Errorf("an empty value must not be written as a stored empty string: %s", step.patch)
	}
}

func TestNodeTaintsValuelessTaintNeedsNoSecondPatch(t *testing.T) {
	node := nodeRepelling(
		[]nodeTaint{notReady, {Key: "guid.foo/drill", Effect: "NoSchedule"}},
		map[string]string{ownedTaintsAnnotation: "guid.foo/drill:NoSchedule"})
	step := decideNodeTaints([]machine.NodeTaint{{Key: "guid.foo/drill", Effect: machine.TaintNoSchedule}}, node)
	if step.patch != nil {
		t.Errorf("a value-less taint should round-trip without growing a value: %s", step.patch)
	}
}

func TestNodeTaintsAdoptionWritesOnlyTheAnnotation(t *testing.T) {
	// The declared taint is already on the Node, so the list needs no
	// rewrite and only the ownership record is missing. A patch that
	// touches no array needs no resourceVersion precondition.
	node := nodeRepelling(
		[]nodeTaint{notReady, {Key: "guid.foo/drill", Value: "node-taints", Effect: "PreferNoSchedule"}}, nil)
	step := decideNodeTaints([]machine.NodeTaint{drillTaint("node-taints")}, node)
	taints, annotations, version := decodeTaintPatch(t, step.patch)
	if taints != nil {
		t.Errorf("a matching list should not be rewritten: %v", taints)
	}
	if annotations[ownedTaintsAnnotation] != "guid.foo/drill:PreferNoSchedule" {
		t.Errorf("annotations: %v", annotations)
	}
	if version != "" {
		t.Errorf("a patch that writes no array needs no precondition: %q", version)
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
	step := decideNodeTaints([]machine.NodeTaint{drillTaint("node-taints")}, nodeRepelling(nil, nil))
	condition := carryOutNodeTaints(c, "node-1", step)
	if condition.Status != api.ConditionTrue || condition.Reason != "Applied" {
		t.Errorf("condition: %+v", condition)
	}
	if len(patched) == 0 {
		t.Error("the patch should have been sent to the Node")
	}
}

func TestCarryOutNodeTaintsReportsAFailedPatch(t *testing.T) {
	// A conflict arrives on this path as a refused patch: the Node
	// moved under the resourceVersion the step named.
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	step := decideNodeTaints([]machine.NodeTaint{drillTaint("node-taints")}, nodeRepelling(nil, nil))
	condition := carryOutNodeTaints(c, "node-1", step)
	if condition.Status != api.ConditionFalse || condition.Reason != "ApplyFailed" {
		t.Errorf("a refused patch should report ApplyFailed: %+v", condition)
	}
}
