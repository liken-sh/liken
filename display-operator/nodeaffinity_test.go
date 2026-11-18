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

// The label display.liken.sh/display=none keeps the DaemonSet's pod
// off a node that has no graphics card. NotIn also matches a node with
// no label. The requirement must be the only term, because the
// scheduler ORs terms and a second term could place the pod on the
// labeled node. It must be the only expression in its term, because
// expressions are ANDed and a second one would also keep the pod off
// nodes with no label.
func TestDaemonSetAffinityExcludesNodesLabeledNone(t *testing.T) {
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
		t.Errorf("the node affinity is %+v, want one term with only %+v", terms, want)
	}
}
