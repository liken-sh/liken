package kubernetes

// The HelmChart read, and the one answer that needs interpreting: a
// cluster whose API server serves no such kind.

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestListHelmChartsReadsTheWholeCluster(t *testing.T) {
	var path string
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kind": "HelmChartList",
			"items": []map[string]any{
				{"metadata": map[string]any{"namespace": "kube-system", "name": "traefik"}},
			},
		})
	}))

	charts, err := ListHelmCharts(client)
	if err != nil {
		t.Fatal(err)
	}
	if len(charts) != 1 || charts[0].Metadata.Name != "traefik" {
		t.Errorf("got %+v", charts)
	}
	if path != HelmChartsPath {
		t.Errorf("charts live in any namespace, so the read covers them all: %s", path)
	}
}

func TestListHelmChartsTreatsAnUnservedKindAsNoCharts(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "the server could not find the requested resource", http.StatusNotFound)
	}))

	charts, err := ListHelmCharts(client)
	if err != nil {
		t.Fatalf("a cluster that never installed the CRD has no charts to wait for: %v", err)
	}
	if len(charts) != 0 {
		t.Errorf("got %+v", charts)
	}
}

func TestListHelmChartsReportsAFailedRead(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))

	if _, err := ListHelmCharts(client); err == nil {
		t.Error("a refused read must never read as an empty cluster")
	}
}
