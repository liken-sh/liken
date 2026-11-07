package main

import (
	"os"
	"strings"
	"testing"
	"time"

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

// A person keeps the pod off a node with no CEC adapter by one label on
// that node. NotIn also matches a node with no such label, so the
// requirement must be the only term: a second term would be ORed with
// it and let the pod back onto the labeled node. It must also be the
// only expression in that term, because a second one narrows where the
// pod runs on a node with no label.
func TestTheDaemonSetStaysOffANodeLabeledNone(t *testing.T) {
	t.Parallel()
	daemonSet := daemonSetIn(t, "deploy/cec.yaml")
	affinity := daemonSet.Spec.Template.Spec.Affinity
	if affinity == nil || affinity.NodeAffinity == nil ||
		affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		t.Fatalf("the DaemonSet %s has no required node affinity", daemonSet.Name)
	}
	terms := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	want := corev1.NodeSelectorRequirement{
		Key:      "equipment.liken.sh/cec",
		Operator: corev1.NodeSelectorOpNotIn,
		Values:   []string{"none"},
	}
	if len(terms) != 1 || len(terms[0].MatchExpressions) != 1 ||
		!equality.Semantic.DeepEqual(terms[0].MatchExpressions[0], want) {
		t.Errorf("the node affinity is %+v, want one term that holds only %+v", terms, want)
	}
}

// The pod watch selects the DaemonSet's pods by their label, and the
// probes reach the listener the DaemonSet names, so the manifest and
// the code must agree.
func TestTheDaemonSetMatchesThePodWatchAndTheProbe(t *testing.T) {
	t.Parallel()
	daemonSet := daemonSetIn(t, "deploy/cec.yaml")
	key, value, _ := strings.Cut(cecPodSelector, "=")
	mustMatch(t, daemonSet.Spec.Template.Labels[key], value)
	container := daemonSet.Spec.Template.Spec.Containers[0]
	environment := map[string]corev1.EnvVar{}
	for _, variable := range container.Env {
		environment[variable.Name] = variable
	}
	mustMatch(t, environment[cecProbeVariable].Value, ":9200")
	mustMatch(t, environment[podNamespaceVariable].ValueFrom.FieldRef.FieldPath, "metadata.namespace")
	for _, probe := range []*corev1.Probe{container.ReadinessProbe, container.LivenessProbe} {
		mustMatch(t, probe.HTTPGet.Path, "/healthz")
		mustMatch(t, probe.HTTPGet.Port.String(), container.Ports[0].Name)
		if time.Duration(probe.TimeoutSeconds)*time.Second <= cecProbeWait {
			t.Errorf("a probe waits %ds, not longer than the handler's %s", probe.TimeoutSeconds, cecProbeWait)
		}
	}
	window := func(probe *corev1.Probe) time.Duration {
		return time.Duration(probe.PeriodSeconds*probe.FailureThreshold) * time.Second
	}
	// A new pod's entry stays current for the readiness window, the
	// time a Ready pod whose loop stops answering takes to lose
	// readiness. The window covers one 30-second request to the API
	// server and a join scan, and the liveness window is longer still.
	mustMatch(t, window(container.ReadinessProbe), cecStartupGrace)
	if window(container.ReadinessProbe) < time.Minute {
		t.Errorf("the readiness window is %s, shorter than a request and a scan", window(container.ReadinessProbe))
	}
	if window(container.LivenessProbe) < 2*window(container.ReadinessProbe) {
		t.Errorf("the liveness window is %s, not clearly longer than the readiness window %s", window(container.LivenessProbe), window(container.ReadinessProbe))
	}
}
