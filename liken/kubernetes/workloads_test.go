package kubernetes

// These tests cover the plugin-workload reader: the label selector the
// requests send across the three kinds, how a workload reports its
// domain and image, and the choice between two workloads that share a
// domain.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// pluginWorkload shapes one workload document with the plugin label
// and a single container image. The shape is the same for a
// Deployment, a DaemonSet, and a StatefulSet.
func pluginWorkload(namespace, name, domain, image string) map[string]any {
	labels := map[string]any{}
	if domain != "" {
		labels[PluginLabel] = domain
	}
	return map[string]any{
		"metadata": map[string]any{"namespace": namespace, "name": name, "labels": labels},
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []map[string]any{{"name": name, "image": image}},
				},
			},
		},
	}
}

// workloadServer answers each kind's list request with its own items,
// keyed by the plural in the request path.
func workloadServer(t *testing.T, byKind map[string][]map[string]any) *apiclient.Client {
	t.Helper()
	return testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items := []map[string]any{}
		for kind, workloads := range byKind {
			if strings.Contains(r.URL.Path, kind) {
				items = workloads
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"kind": "List", "items": items})
	}))
}

func TestListPluginWorkloadsFindsADaemonSetOnlyOperator(t *testing.T) {
	client := workloadServer(t, map[string][]map[string]any{
		"deployments":  {pluginWorkload("audio", "audio-operator", "audio", "ghcr.io/liken-sh/audio-operator:2026.09.03-007")},
		"daemonsets":   {pluginWorkload("bluetooth", "bluetooth-operator", "bluetooth", "ghcr.io/liken-sh/bluetooth-operator:2026.09.03-007")},
		"statefulsets": {},
	})

	workloads, err := ListPluginWorkloads(client)
	if err != nil {
		t.Fatalf("listing the plugin workloads: %v", err)
	}
	domains := map[string]string{}
	for _, w := range workloads {
		domains[w.PluginDomain()] = w.OperatorImage()
	}
	if _, ok := domains["bluetooth"]; !ok {
		t.Fatalf("the domains are %v, want the one that only a DaemonSet runs", domains)
	}
	if _, ok := domains["audio"]; !ok {
		t.Fatalf("the domains are %v, want the one that a Deployment runs", domains)
	}
}

func TestListPluginWorkloadsSelectsOnTheLabel(t *testing.T) {
	var gotQuery string
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{"kind": "List", "items": []map[string]any{}})
	}))
	if _, err := ListPluginWorkloads(client); err != nil {
		t.Fatalf("listing the plugin workloads: %v", err)
	}
	if !strings.Contains(gotQuery, "labelSelector=cli.liken.sh") {
		t.Fatalf("the query is %q, want a labelSelector on the plugin label", gotQuery)
	}
}

func TestDedupeByDomainPrefersTheOperatorImage(t *testing.T) {
	var operatorFirst, sidecarFirst Workload
	operatorFirst.Metadata.Labels = map[string]string{PluginLabel: "audio"}
	operatorFirst.Spec.Template.Spec.Containers = []WorkloadContainer{{Image: "ghcr.io/liken-sh/audio-operator:2026.09.03-007"}}
	sidecarFirst.Metadata.Labels = map[string]string{PluginLabel: "audio"}
	sidecarFirst.Spec.Template.Spec.Containers = []WorkloadContainer{{Image: "ghcr.io/liken-sh/some-sidecar:1"}}

	got := dedupeByDomain([]Workload{sidecarFirst, operatorFirst})
	if len(got) != 1 {
		t.Fatalf("the list has %d workloads, want 1 for the one domain", len(got))
	}
	if got[0].OperatorImage() != "ghcr.io/liken-sh/audio-operator:2026.09.03-007" {
		t.Fatalf("the operator image is %q, want the operator container's image", got[0].OperatorImage())
	}
}

func TestWorkloadReportsNoImageWithNoContainers(t *testing.T) {
	var w Workload
	if got := w.OperatorImage(); got != "" {
		t.Errorf("the image of a workload with no containers is %q, want empty", got)
	}
	if got := w.PluginDomain(); got != "" {
		t.Errorf("the domain of a workload with no labels is %q, want empty", got)
	}
}

func TestListPluginWorkloadsReportsAFailedRead(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "the server is currently unable to handle the request", http.StatusServiceUnavailable)
	}))
	if _, err := ListPluginWorkloads(client); err == nil {
		t.Error("a failed read returned no error, want one")
	}
}
