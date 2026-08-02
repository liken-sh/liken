package main

// Node taints, reconciled live from the Machine spec.
//
// A label attracts a workload and a taint repels one, so
// spec.nodeTaints is the other half of the scheduling identity that
// labels.go applies. It reaches the Node the same two ways. Init
// renders the taints into the k3s boot drop-in, so a node registers
// already repelling, which is what keeps a fresh node from accepting,
// in its first minutes, the pods the taint exists to keep out. But
// the kubelet applies registration taints only when it creates the
// Node object, on a first boot or after a reinstall. On every later
// boot the Node already exists and the setting does nothing, so live
// reconciliation is the only mechanism from then on. It runs here, in
// the same pass that reconciles labels.
//
// Removing a taint needs a record, for the reason labels.go gives:
// nothing about a taint on a Node says who applied it, and the
// operator must never remove one that a person or another controller
// applied.

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// ownedTaintsAnnotation records, on the Node itself, which taints
// liken manages. Its value is the pairs it owns, each written
// key:Effect, sorted and joined with commas. It lives on the Node
// rather than in the Machine's status, so the record and the taints
// it describes can never drift apart across operator restarts or
// Machine rewrites.
//
// The pair is the unit of ownership, where labels own a bare key. A
// taint's identity is its key together with its effect: one key can
// carry NoSchedule and NoExecute at the same time, and those are two
// separate taints that a pod tolerates separately. A record keyed on
// the key alone would give the operator permission to remove a taint
// it never applied, whenever someone added a second effect under a
// declared key.
const ownedTaintsAnnotation = "liken.sh/node-taints"

// A taintStep is one pass's worth of taint reconciliation: the Node
// patch to apply (nil when the Node already matches the spec) and the
// condition to publish once the patch lands.
type taintStep struct {
	patch     []byte
	condition api.Condition
}

// taintPair renders a taint's identity, its key and its effect, in
// the form the ownership annotation stores.
func taintPair(key, effect string) string {
	return key + ":" + effect
}

// decideNodeTaints compares the spec's taints against the Node and
// produces the merge patch that closes the gap. It upserts every
// declared taint, drops every pair the annotation owns that the spec
// no longer declares, and keeps every other taint exactly as it
// found it. A taint whose pair is in neither the spec nor the
// annotation belongs to someone else, and this function never
// changes it.
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

	// The merged list is what the Node must hold after the patch. A
	// taint the Node already carries keeps the position it has, and a
	// newly declared one goes on the end in sorted order. Position
	// means nothing to the scheduler, so the only requirement is that
	// the same inputs always produce the same list. A list whose
	// order moved from pass to pass would patch the Node forever.
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
		Message: fmt.Sprintf("the Node carries all %d declared taints", len(desired))}
	if len(desired) == 0 {
		condition = api.Condition{Type: "NodeTaintsApplied", Status: api.ConditionTrue, Reason: "NothingDeclared",
			Message: "no node taints declared"}
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
		// A JSON merge patch replaces an array whole, where it merges
		// a map key by key. So the taints write must carry the full
		// list, foreign entries included, and a write that starts
		// from a stale read erases whatever landed in between. The
		// node lifecycle controller writes this same array: it adds
		// node.kubernetes.io/not-ready and unreachable when a node
		// stops reporting, and removes them when it recovers. Naming
		// the resourceVersion this pass read turns that race into a
		// 409 conflict, so the API server refuses the write instead
		// of dropping the controller's taint. The next pass reads the
		// Node again and patches again against the new version.
		//
		// The labels patch needs no such precondition, because a
		// merge patch on a map touches only the keys it names, and a
		// concurrent writer's key is not one of them.
		metadata["resourceVersion"] = node.Metadata.ResourceVersion
		patch["spec"] = map[string]any{"taints": merged}
	}
	patch["metadata"] = metadata
	encoded, _ := json.Marshal(patch)
	return taintStep{patch: encoded, condition: condition}
}

// carryOutNodeTaints applies the step's patch. It downgrades the
// condition when the API server refuses the patch. A refusal here is
// often the resourceVersion conflict, and the recovery is the same as
// for any other failure: the next pass reads the Node again, builds
// the step again, and patches again.
func carryOutNodeTaints(c *kubernetes.Client, name string, step taintStep) api.Condition {
	if step.patch == nil {
		return step.condition
	}
	if err := c.PatchJSON(nodesPath+"/"+name, step.patch); err != nil {
		return api.Condition{Type: "NodeTaintsApplied", Status: api.ConditionFalse, Reason: "ApplyFailed",
			Message: fmt.Sprintf("patching the Node's taints: %v", err)}
	}
	return step.condition
}
