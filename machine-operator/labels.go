package main

// Node labels, reconciled live from the Machine spec.
//
// Workloads schedule on node labels, so spec.nodeLabels is a
// machine's scheduling identity, and it reaches the Node twice. Init
// renders the labels into the k3s boot drop-in so the node registers
// already wearing them, which covers a machine's first moments. But
// registration is one-way: the kubelet applies labels and never
// removes one, so a label retracted from the spec would linger on the
// Node forever. Live reconciliation belongs here, in the same pass
// that re-asserts sysctls.
//
// Removal needs memory. Nothing about a label on a Node says who put
// it there, and the operator must never strip one a person or another
// controller applied. The memory is an annotation on the Node
// recording exactly the keys this operator manages: when a key sits
// in the annotation but not in the spec, that difference is the
// license to remove it. This is the same trick the drain uses to tell
// its own cordon from a human's.

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/chrisguidry/liken/kubernetes"
	"github.com/chrisguidry/liken/machine"
)

// ownedLabelsAnnotation records, on the Node itself, which label keys
// liken manages: the spec's keys, sorted and comma-joined. It lives
// on the Node rather than in the Machine's status so the record and
// the labels it describes can never drift apart across operator
// restarts or Machine rewrites.
const ownedLabelsAnnotation = "liken.sh/node-labels"

// A labelStep is one pass's worth of label reconciliation: the Node
// patch to apply (nil when the Node already agrees with the spec) and
// the condition to publish once it lands.
type labelStep struct {
	patch     []byte
	condition machine.Condition
}

// decideNodeLabels compares the spec's labels against the Node and
// produces the merge patch that closes the gap: missing and drifted
// labels re-asserted, retracted ones erased (merge-patch null is how
// a key is deleted), and the ownership annotation kept agreeing with
// the spec. Labels outside both the spec and the annotation are
// someone else's and are never touched.
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

	condition := machine.Condition{Type: "NodeLabelsApplied", Status: machine.ConditionTrue, Reason: "Applied",
		Message: fmt.Sprintf("the Node carries all %d declared labels", len(desired))}
	if len(desired) == 0 {
		condition = machine.Condition{Type: "NodeLabelsApplied", Status: machine.ConditionTrue, Reason: "NothingDeclared",
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

// carryOutNodeLabels applies the step's patch, downgrading the
// condition when the API server refuses it; the next pass re-decides
// from a fresh read and tries again.
func carryOutNodeLabels(c *kubernetes.Client, name string, step labelStep) machine.Condition {
	if step.patch == nil {
		return step.condition
	}
	if err := c.PatchJSON(nodesPath+"/"+name, step.patch); err != nil {
		return machine.Condition{Type: "NodeLabelsApplied", Status: machine.ConditionFalse, Reason: "ApplyFailed",
			Message: fmt.Sprintf("patching the Node's labels: %v", err)}
	}
	return step.condition
}
