package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/yaml"
)

// documentsOf decodes each document of a manifest file whose kind
// matches into T.
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
		t.Fatalf("%s holds no %s", path, kind)
	}
	return out
}

// A person keeps the pod off a node with no GPU by one label on that
// node. NotIn also matches a node with no such label, so the
// requirement must be the only term and the only expression in it.
func TestTheDaemonSetStaysOffANodeLabeledNone(t *testing.T) {
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
		t.Errorf("the node affinity is %+v, want one term that holds only %+v", terms, want)
	}
}

// The agent finds its claim in its pod's status by the name the pod
// spec gives it.
func TestThePodNamesTheClaimTheAgentReads(t *testing.T) {
	daemonSet := documentsOf[appsv1.DaemonSet](t, "../deploy/capabilities.yaml", "DaemonSet")[0]
	claims := daemonSet.Spec.Template.Spec.ResourceClaims
	if len(claims) != 1 || claims[0].Name != claimName {
		t.Errorf("the pod's claims are %+v, want one named %s", claims, claimName)
	}
}

// Every attribute a shipped class reads must be one the agent
// publishes, because a selector that reads an absent attribute fails
// to evaluate, and the claim never allocates.
func TestEveryClassReadsAnAttributeTheAgentPublishes(t *testing.T) {
	published := capabilitiesOf(report{})
	attribute := regexp.MustCompile(`device\.attributes\["media\.liken\.sh"\]\.(\w+)`)
	for _, class := range documentsOf[resourcev1.DeviceClass](t, "../deploy/capability-classes.yaml", "DeviceClass") {
		for _, selector := range class.Spec.Selectors {
			for _, match := range attribute.FindAllStringSubmatch(selector.CEL.Expression, -1) {
				if _, ok := published[match[1]]; !ok {
					t.Errorf("the class %s reads %s, which the agent does not publish", class.Name, match[1])
				}
			}
		}
	}
}
