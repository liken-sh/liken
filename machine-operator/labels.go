package main

// Node labels, reconciled live from the Machine spec.
//
// Workloads schedule based on node labels, so spec.nodeLabels sets a
// machine's scheduling identity, and it reaches the Node in two
// ways. Init renders the labels into the k3s boot drop-in, so the
// node registers with them already applied, which covers a
// machine's first moments. But registration only adds labels: the
// kubelet applies labels and never removes one, so a label taken out
// of the spec would stay on the Node forever. Live reconciliation
// belongs here, in the same pass that re-applies sysctls.
//
// Removing a label needs a record. Nothing about a label on a Node
// says who put it there, and the operator must never remove one that
// a person or another controller applied. The record is an
// annotation on the Node that lists exactly the keys this operator
// manages. When a key is in the annotation but not in the spec, that
// difference tells the operator to remove it. This is the same
// method the drain uses to tell its own cordon apart from a
// person's.

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/kubernetes"
)

// ownedLabelsAnnotation records, on the Node itself, which label
// keys liken manages. Its value is the spec's keys, sorted and
// joined with commas. It lives on the Node rather than in the
// Machine's status, so the record and the labels it describes can
// never drift apart across operator restarts or Machine rewrites.
const ownedLabelsAnnotation = "liken.sh/node-labels"

// A labelStep is one pass's worth of label reconciliation: the Node
// patch to apply (nil when the Node already matches the spec) and
// the condition to publish once the patch lands.
type labelStep struct {
	patch     []byte
	condition api.Condition
}

// decideNodeLabels compares the spec's labels against the Node and
// produces the merge patch that closes the gap. It reapplies
// missing and changed labels, deletes removed ones (a null value in
// a merge patch deletes the key), and keeps the ownership annotation
// matching the spec. Labels outside both the spec and the
// annotation belong to someone else, and the function never touches
// them.
func decideNodeLabels(desired map[string]string, node *nodeObject) labelStep {
	labels := map[string]any{}
	for key, value := range desired {
		if node.Metadata.Labels[key] != value {
			labels[key] = value
		}
	}
	for owned := range strings.SplitSeq(node.Metadata.Annotations[ownedLabelsAnnotation], ",") {
		if owned == "" {
			continue
		}
		if _, still := desired[owned]; still {
			continue
		}
		if _, present := node.Metadata.Labels[owned]; present {
			labels[owned] = nil
		}
	}

	annotations := map[string]any{}
	ownedNow := strings.Join(slices.Sorted(maps.Keys(desired)), ",")
	if ownedNow != node.Metadata.Annotations[ownedLabelsAnnotation] {
		if ownedNow == "" {
			annotations[ownedLabelsAnnotation] = nil
		} else {
			annotations[ownedLabelsAnnotation] = ownedNow
		}
	}

	condition := api.Condition{Type: "NodeLabelsApplied", Status: api.ConditionTrue, Reason: "Applied",
		Message: fmt.Sprintf("the Node carries all %d declared labels", len(desired))}
	if len(desired) == 0 {
		condition = api.Condition{Type: "NodeLabelsApplied", Status: api.ConditionTrue, Reason: "NothingDeclared",
			Message: "no node labels declared"}
	}

	if len(labels) == 0 && len(annotations) == 0 {
		return labelStep{condition: condition}
	}
	metadata := map[string]any{}
	if len(labels) > 0 {
		metadata["labels"] = labels
	}
	if len(annotations) > 0 {
		metadata["annotations"] = annotations
	}
	patch, _ := json.Marshal(map[string]any{"metadata": metadata})
	return labelStep{patch: patch, condition: condition}
}

// carryOutNodeLabels applies the step's patch. It downgrades the
// condition when the API server refuses the patch. The next pass
// reads the Node again, decides again, and tries again.
func carryOutNodeLabels(c *kubernetes.Client, name string, step labelStep) api.Condition {
	if step.patch == nil {
		return step.condition
	}
	if err := c.PatchJSON(nodesPath+"/"+name, step.patch); err != nil {
		return api.Condition{Type: "NodeLabelsApplied", Status: api.ConditionFalse, Reason: "ApplyFailed",
			Message: fmt.Sprintf("patching the Node's labels: %v", err)}
	}
	return step.condition
}
