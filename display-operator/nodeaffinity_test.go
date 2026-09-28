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

// daemonSetIn is the one DaemonSet in the manifest file.
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
	t.Fatalf("%s holds no DaemonSet", path)
	return nil
}

// A person keeps the pod off a node with no graphics card by one label on
// that node. NotIn also matches a node with no such label, so the
// requirement must be the only term: a second term would be ORed with
// it and let the pod back onto the labeled node. It must also be the
// only expression in that term, because a second one narrows where the
// pod runs on a node with no label.
func TestTheDaemonSetStaysOffANodeLabeledNone(t *testing.T) {
	daemonSet := daemonSetIn(t, "deploy/operator.yaml")
	affinity := daemonSet.Spec.Template.Spec.Affinity
	if affinity == nil || affinity.NodeAffinity == nil ||
		affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		t.Fatalf("the DaemonSet %s has no required node affinity", daemonSet.Name)
	}
	terms := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	want := corev1.NodeSelectorRequirement{
		Key:      "display.liken.sh/display",
		Operator: corev1.NodeSelectorOpNotIn,
		Values:   []string{"none"},
	}
	if len(terms) != 1 || len(terms[0].MatchExpressions) != 1 ||
		!equality.Semantic.DeepEqual(terms[0].MatchExpressions[0], want) {
		t.Errorf("the node affinity is %+v, want one term that holds only %+v", terms, want)
	}
}
