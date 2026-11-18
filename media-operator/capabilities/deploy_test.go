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

// documentsOf decodes every YAML document of kind `kind` in the file
// into a T, and fails the test when there is none.
func documentsOf[T any](t *testing.T, path, kind string) []T {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var out []T
	for _, document := range strings.Split(string(raw), "\n---\n") {
		if !strings.Contains(document, "\nkind: "+kind+"\n") {
			continue
		}
		var found T
		if err := yaml.UnmarshalStrict([]byte(document), &found); err != nil {
			t.Fatalf("decoding a %s in %s: %v", kind, path, err)
		}
		out = append(out, found)
	}
	if len(out) == 0 {
		t.Fatalf("%s has no %s document", path, kind)
	}
	return out
}

// An administrator labels a node that has no GPU
// media.liken.sh/gpu=none to keep the pod off it, and every other node
// must get a pod. Node selector terms are ORed, so a second term could
// place the pod on a node labeled none. A second expression in the term
// could keep the pod off an unlabeled node.
func TestDaemonSetAffinityExcludesOnlyNodesLabeledGpuNone(t *testing.T) {
	daemonSet := documentsOf[appsv1.DaemonSet](t, "../deploy/capabilities.yaml", "DaemonSet")[0]
	affinity := daemonSet.Spec.Template.Spec.Affinity
	if affinity == nil || affinity.NodeAffinity == nil ||
		affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		t.Fatalf("the DaemonSet %s has no required node affinity", daemonSet.Name)
	}
	terms := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	want := corev1.NodeSelectorRequirement{Key: "media.liken.sh/gpu", Operator: corev1.NodeSelectorOpNotIn, Values: []string{"none"}}
	if len(terms) != 1 || len(terms[0].MatchExpressions) != 1 ||
		!equality.Semantic.DeepEqual(terms[0].MatchExpressions[0], want) {
		t.Errorf("the node affinity is %+v, want one term with only %+v", terms, want)
	}
}

// The agent finds its claim in the pod's status by claimName, so the
// pod spec must declare its claim under that name.
func TestPodDeclaresTheClaimTheAgentReads(t *testing.T) {
	daemonSet := documentsOf[appsv1.DaemonSet](t, "../deploy/capabilities.yaml", "DaemonSet")[0]
	claims := daemonSet.Spec.Template.Spec.ResourceClaims
	if len(claims) != 1 || claims[0].Name != claimName {
		t.Errorf("the pod's claims are %+v, want one named %s", claims, claimName)
	}
}
