package main

// Reconciles the Node's taints with spec.nodeTaints in the Machine, in
// the same pass that reconciles labels (labels.go).
//
// Init writes the taints into the k3s boot drop-in, so a new node has
// them from the moment it registers.
// The kubelet applies registration taints only when it creates the
// Node object, on a first boot or after a reinstall. On every later
// boot the Node already exists, so only this reconciliation changes its
// taints.
//
// A taint on a Node does not record who applied it, so the operator
// records the taints that it applied in ownedTaintsAnnotation, and
// never removes one that a person or another controller applied.

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// ownedTaintsAnnotation is the annotation on the Node that lists the
// taints the operator applied, as a comma-separated, sorted list of
// key:Effect pairs. The record is on the Node, not in the Machine's
// status, so it stays consistent with the taints it describes when the
// operator restarts or the Machine is rewritten.
//
// A taint is owned by its key and effect, where a label is owned by its
// key alone. One key can have both a NoSchedule and a NoExecute taint,
// and a pod tolerates each one separately. If the record held only the
// key, the operator would remove a taint it never applied whenever
// someone added a second effect under a declared key.
const ownedTaintsAnnotation = "liken.sh/node-taints"

// A taintStep is what decideNodeTaints returns: the Node patch, nil
// when the Node already matches the spec, and the condition to publish
// once the patch succeeds.
type taintStep struct {
	patch     []byte
	condition api.Condition
}

// taintPair returns the key:Effect form that ownedTaintsAnnotation
// stores.
func taintPair(key, effect string) string {
	return key + ":" + effect
}

// decideNodeTaints compares the spec's taints with the Node's and
// returns the merge patch that makes them match. It adds or updates
// each declared taint and removes each taint that the annotation lists
// but the spec no longer declares. A taint whose pair is in neither
// the spec nor the annotation belongs to someone else, and the
// function leaves it unchanged.
func decideNodeTaints(desired []machine.NodeTaint, node *nodeObject) taintStep {
	wanted := map[string]nodeTaint{}
	for _, taint := range desired {
		pair := taintPair(taint.Key, string(taint.Effect))
		wanted[pair] = nodeTaint{Key: taint.Key, Value: taint.Value, Effect: string(taint.Effect)}
	}
	owned := map[string]bool{}
	for pair := range strings.SplitSeq(node.Metadata.Annotations[ownedTaintsAnnotation], ",") {
		if pair != "" {
			owned[pair] = true
		}
	}

	// merged is the taint list the Node must have after the patch.
	// Taints already on the Node keep their positions and new ones go
	// on the end in sorted order. The scheduler ignores order, but if
	// the order changed from pass to pass, each pass would patch the
	// Node again.
	merged := []nodeTaint{}
	placed := map[string]bool{}
	for _, on := range node.Spec.Taints {
		pair := taintPair(on.Key, on.Effect)
		if declared, is := wanted[pair]; is {
			merged = append(merged, declared)
			placed[pair] = true
			continue
		}
		if owned[pair] {
			continue
		}
		merged = append(merged, on)
	}
	for _, pair := range slices.Sorted(maps.Keys(wanted)) {
		if !placed[pair] {
			merged = append(merged, wanted[pair])
		}
	}

	annotations := map[string]any{}
	ownedNow := strings.Join(slices.Sorted(maps.Keys(wanted)), ",")
	if ownedNow != node.Metadata.Annotations[ownedTaintsAnnotation] {
		if ownedNow == "" {
			annotations[ownedTaintsAnnotation] = nil
		} else {
			annotations[ownedTaintsAnnotation] = ownedNow
		}
	}

	condition := api.Condition{Type: "NodeTaintsApplied", Status: api.ConditionTrue, Reason: "Applied",
		Message: fmt.Sprintf("The Node has all %d declared taints", len(desired))}
	if len(desired) == 0 {
		condition = api.Condition{Type: "NodeTaintsApplied", Status: api.ConditionTrue, Reason: "NothingDeclared",
			Message: "No node taints are declared"}
	}

	rewrite := !slices.Equal(merged, node.Spec.Taints)
	if !rewrite && len(annotations) == 0 {
		return taintStep{condition: condition}
	}

	patch := map[string]any{}
	metadata := map[string]any{}
	if len(annotations) > 0 {
		metadata["annotations"] = annotations
	}
	if rewrite {
		// A JSON merge patch replaces an array whole, but merges a map
		// key by key. So this patch must contain the full taint list,
		// including taints that other writers own, and a patch built
		// from a stale read would erase their changes. The node
		// lifecycle controller writes the same array: it adds
		// node.kubernetes.io/not-ready and unreachable when a node
		// stops reporting and removes them when it recovers. With the
		// resourceVersion that this pass read, the API server rejects a
		// stale patch with 409 Conflict. The Node watch then starts the
		// next pass, which patches again from the new version.
		//
		// The labels patch needs no such precondition, because it
		// changes only the map keys that it names, and no other writer
		// owns those keys.
		metadata["resourceVersion"] = node.Metadata.ResourceVersion
		patch["spec"] = map[string]any{"taints": merged}
	}
	patch["metadata"] = metadata
	encoded, _ := json.Marshal(patch)
	return taintStep{patch: encoded, condition: condition}
}

// carryOutNodeTaints applies the step's patch and returns the
// condition to publish. When the patch fails, for example on a
// resourceVersion conflict, it returns a False condition, and the next
// pass reads the Node and patches again.
func carryOutNodeTaints(c *apiclient.Client, name string, step taintStep) api.Condition {
	if step.patch == nil {
		return step.condition
	}
	if err := kubernetes.PatchJSON(c, nodesPath+"/"+name, step.patch); err != nil {
		return api.Condition{Type: "NodeTaintsApplied", Status: api.ConditionFalse, Reason: "ApplyFailed",
			Message: fmt.Sprintf("The Node does not have the declared taints: %v", err)}
	}
	return step.condition
}
