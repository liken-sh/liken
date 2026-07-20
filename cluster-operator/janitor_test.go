package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/liken-sh/liken/cluster"
)

func featureDaemonSet(name, slug string) featureWorkload {
	w := featureWorkload{}
	w.Metadata.Name = name
	if slug != "" {
		w.Metadata.Annotations = map[string]string{featureAnnotation: slug}
	}
	return w
}

func retractionNames(workloads []featureWorkload) []string {
	names := make([]string, 0, len(workloads))
	for _, w := range workloads {
		names = append(names, w.Metadata.Name)
	}
	return names
}

func TestJanitorLeavesDeclaredFeatureWorkloads(t *testing.T) {
	features := map[string]*cluster.FeatureConfig{"iscsi": {}}
	workloads := []featureWorkload{featureDaemonSet("liken-iscsid", "iscsi")}
	if got := decideRetractions(features, workloads); len(got) != 0 {
		t.Fatalf("expected no retractions, got %v", retractionNames(got))
	}
}

func TestJanitorDeletesRetractedFeatureWorkloads(t *testing.T) {
	workloads := []featureWorkload{featureDaemonSet("liken-iscsid", "iscsi")}
	got := decideRetractions(nil, workloads)
	if names := retractionNames(got); len(names) != 1 || names[0] != "liken-iscsid" {
		t.Fatalf("expected [liken-iscsid], got %v", names)
	}
}

func TestJanitorJudgesEachWorkloadByItsOwnFeature(t *testing.T) {
	features := map[string]*cluster.FeatureConfig{"nfs": {}}
	workloads := []featureWorkload{
		featureDaemonSet("liken-iscsid", "iscsi"),
		featureDaemonSet("liken-nfs-helper", "nfs"),
	}
	got := decideRetractions(features, workloads)
	if names := retractionNames(got); len(names) != 1 || names[0] != "liken-iscsid" {
		t.Fatalf("expected [liken-iscsid], got %v", names)
	}
}

func TestJanitorIgnoresWorkloadsWithoutTheAnnotation(t *testing.T) {
	// The operator and log-relay DaemonSets live in liken-system too.
	// They carry no feature annotation, which means no feature owns
	// them, and the janitor must never touch them.
	workloads := []featureWorkload{featureDaemonSet("liken-machine-operator", "")}
	if got := decideRetractions(nil, workloads); len(got) != 0 {
		t.Fatalf("expected no retractions, got %v", retractionNames(got))
	}
}

func TestJanitorDeletesRetractedWorkloadsThroughTheAPI(t *testing.T) {
	var deletes []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/daemonsets"):
			list := struct {
				Items []featureWorkload `json:"items"`
			}{Items: []featureWorkload{
				featureDaemonSet("liken-iscsid", "iscsi"),
				featureDaemonSet("liken-machine-operator", ""),
			}}
			_ = json.NewEncoder(w).Encode(list)
		case r.Method == http.MethodDelete:
			deletes = append(deletes, r.URL.Path)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	clusterDoc := &cluster.Cluster{}
	janitorFeatureWorkloads(testClient(t, handler), clusterDoc)
	want := "/apis/apps/v1/namespaces/liken-system/daemonsets/liken-iscsid"
	if len(deletes) != 1 || deletes[0] != want {
		t.Fatalf("expected exactly [%s] deleted, got %v", want, deletes)
	}
}
