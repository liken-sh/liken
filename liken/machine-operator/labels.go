package main

// The operator keeps the labels of spec.nodeLabels on the running Node.
//
// init writes spec.nodeLabels into the k3s drop-in (init/k3s.go), so
// the node registers with them. Registration only adds labels, and the
// kubelet never removes one, so a label deleted from the spec would stay
// on the Node. Each pass therefore patches the Node to match the spec.
//
// The operator removes only labels that it set, never one that a person
// or another controller set. The annotation ownedLabelsAnnotation on the
// Node lists the keys it set, so a key in the annotation but not in the
// spec is one to remove.

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/kubernetes"
)

// ownedLabelsAnnotation holds the spec's label keys, sorted and joined
// with commas. It is on the Node, not in the Machine's status, so the
// same patch writes it and the labels, and a rewrite of the Machine
// cannot lose it.
const ownedLabelsAnnotation = "liken.sh/node-labels"

// labelStep is the merge patch for the Node, nil when the Node already
// matches the spec, and the condition to publish once the patch applies.
type labelStep struct {
	patch     []byte
	condition api.Condition
}

// decideNodeLabels returns the merge patch that sets missing and
// changed labels, deletes each label that the annotation lists and the
// spec does not, and updates the annotation. A null value in a merge
// patch deletes the key.
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
		Message: fmt.Sprintf("The Node has all %d declared labels", len(desired))}
	if len(desired) == 0 {
		condition = api.Condition{Type: "NodeLabelsApplied", Status: api.ConditionTrue, Reason: "NothingDeclared",
			Message: "No node labels are declared"}
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

// carryOutNodeLabels applies the step's patch to the Node. When the
// patch fails, it returns a False condition, and the next pass decides
// again from a fresh read of the Node.
func carryOutNodeLabels(c *apiclient.Client, name string, step labelStep) api.Condition {
	if step.patch == nil {
		return step.condition
	}
	if err := kubernetes.PatchJSON(c, nodesPath+"/"+name, step.patch); err != nil {
		return api.Condition{Type: "NodeLabelsApplied", Status: api.ConditionFalse, Reason: "ApplyFailed",
			Message: fmt.Sprintf("The Node does not have the declared labels: %v", err)}
	}
	return step.condition
}
