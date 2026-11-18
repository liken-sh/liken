package main

import (
	"os"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/yaml"
)

// daemonSetIn returns the first DaemonSet in the manifest file.
func daemonSetIn(t *testing.T, path string) *appsv1.DaemonSet {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	for _, document := range strings.Split(string(raw), "\n---\n") {
		if !strings.Contains(document, "kind: DaemonSet") {
			continue
		}
		found := &appsv1.DaemonSet{}
		if err := yaml.UnmarshalStrict([]byte(document), found); err != nil {
			t.Fatalf("decoding the DaemonSet in %s: %v", path, err)
		}
		return found
	}
	t.Fatalf("%s has no DaemonSet", path)
	return nil
}

// The label audio.liken.sh/sound-card=none keeps the DaemonSet's pod
// off a node, and NotIn also matches a node with no such label. The
// scheduler ORs the terms of a node affinity, so a second term could let
// the pod onto a labeled node. It ANDs the expressions in a term, so a
// second expression could keep the pod off an unlabeled node.
func TestTheDaemonSetStaysOffANodeLabeledNone(t *testing.T) {
	daemonSet := daemonSetIn(t, "deploy/operator.yaml")
	affinity := daemonSet.Spec.Template.Spec.Affinity
	if affinity == nil || affinity.NodeAffinity == nil ||
		affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		t.Fatalf("the DaemonSet %s has no required node affinity, want one", daemonSet.Name)
	}
	terms := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	want := corev1.NodeSelectorRequirement{
		Key:      "audio.liken.sh/sound-card",
		Operator: corev1.NodeSelectorOpNotIn,
		Values:   []string{"none"},
	}
	if len(terms) != 1 || len(terms[0].MatchExpressions) != 1 ||
		!equality.Semantic.DeepEqual(terms[0].MatchExpressions[0], want) {
		t.Errorf("the node affinity is %+v, want one term that contains only %+v", terms, want)
	}
}
