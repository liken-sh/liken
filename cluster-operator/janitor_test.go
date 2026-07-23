package main

import (
	"encoding/json"
	"net/http"
	"slices"
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

func TestJanitorFluxLeavesADeclaredFeatureAlone(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no API call should happen: %s %s", r.Method, r.URL.Path)
	}))
	janitorFlux(c, fluxCluster())
}

// Stage 1: the controllers die first, and nothing else is touched on
// the same pass. This ordering is the safety property: a controller
// that processed a sync object's deletion would garbage-collect
// everything the repository ever applied.
func TestJanitorFluxKillsTheControllersFirst(t *testing.T) {
	var deleted []string
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`{"metadata": {"name": "x"}}`))
		case http.MethodDelete:
			deleted = append(deleted, r.URL.Path)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected call: %s %s", r.Method, r.URL.Path)
		}
	}))
	plain := &cluster.Cluster{}
	plain.Metadata.Name = "lab"
	janitorFlux(c, plain)
	want := []string{
		"/apis/apps/v1/namespaces/flux-system/deployments/source-controller",
		"/apis/apps/v1/namespaces/flux-system/deployments/kustomize-controller",
	}
	if !slices.Equal(deleted, want) {
		t.Errorf("deleted %v, want exactly the two Deployments", deleted)
	}
}

// Stage 2: deployments gone, but a controller pod still terminating
// means nothing more happens this pass.
func TestJanitorFluxWaitsOutTerminatingControllers(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/deployments/"):
			http.NotFound(w, r)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pods"):
			w.Write([]byte(`{"items": [{"metadata": {"name": "kustomize-controller-x"}}]}`))
		default:
			t.Errorf("unexpected call: %s %s", r.Method, r.URL.Path)
		}
	}))
	plain := &cluster.Cluster{}
	plain.Metadata.Name = "lab"
	janitorFlux(c, plain)
}

// Stage 3: with the controllers provably gone, the sync objects lose
// their finalizers and everything else goes, the namespace and the
// deploy key with it.
func TestJanitorFluxTearsDownOnceControllersAreGone(t *testing.T) {
	var patched, deleted []string
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/deployments/"):
			http.NotFound(w, r)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pods"):
			w.Write([]byte(`{"items": []}`))
		case r.Method == http.MethodPatch:
			patched = append(patched, r.URL.Path)
			w.Write([]byte(`{}`))
		case r.Method == http.MethodDelete:
			deleted = append(deleted, r.URL.Path)
			w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected call: %s %s", r.Method, r.URL.Path)
		}
	}))
	plain := &cluster.Cluster{}
	plain.Metadata.Name = "lab"
	janitorFlux(c, plain)
	if len(patched) != 2 {
		t.Errorf("both sync objects lose their finalizers, got %v", patched)
	}
	if !slices.Equal(deleted, fluxTeardownPaths) {
		t.Errorf("the teardown must cover every path, in order:\n got %v\nwant %v", deleted, fluxTeardownPaths)
	}
}
