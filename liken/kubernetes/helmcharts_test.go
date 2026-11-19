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
		t.Fatalf("listing the HelmCharts: %v", err)
	}
	if len(charts) != 1 || charts[0].Metadata.Name != "traefik" {
		t.Errorf("the charts are %+v, want one chart named traefik", charts)
	}
	if path != HelmChartsPath {
		t.Errorf("the request path is %s, want %s because charts live in any namespace", path, HelmChartsPath)
	}
}

func TestListHelmChartsTreatsAnUnservedKindAsNoCharts(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "the server could not find the requested resource", http.StatusNotFound)
	}))

	charts, err := ListHelmCharts(client)
	if err != nil {
		t.Fatalf("ListHelmCharts returned the error %v, want none because a cluster without the CRD has no charts to wait for", err)
	}
	if len(charts) != 0 {
		t.Errorf("the charts are %+v, want none", charts)
	}
}

func TestListHelmChartsReportsAFailedRead(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))

	if _, err := ListHelmCharts(client); err == nil {
		t.Error("a 403 returned no error, want an error because a refused read must not read as an empty cluster")
	}
}
