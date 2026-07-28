package main

import (
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/api"
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

// retractedCluster is a cluster that declares no features at all, so
// every flux slug is retracted and the janitor has work to consider.
func retractedCluster() *cluster.Cluster {
	c := &cluster.Cluster{}
	c.Metadata.Name = "lab"
	return c
}

// plantedNamespace is the flux-system Namespace as liken's planter
// creates it: stamped with the feature it belongs to. An installation
// somebody else made carries no such annotation.
const plantedNamespace = `{"metadata": {"name": "flux-system",
	"annotations": {"liken.sh/feature": "flux"}}}`

func TestJanitorFluxLeavesADeclaredFeatureAlone(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no API call should happen: %s %s", r.Method, r.URL.Path)
	}))
	if got := janitorFlux(c, fluxCluster()); got != nil {
		t.Errorf("a declared feature has nothing to report: %+v", got)
	}
}

// The finding this gate exists for: an adopted cluster already ran
// its own Flux, and the adopting document never named the slug. liken
// did not plant that installation, so liken deletes none of it.
func TestJanitorFluxDeletesNothingItDidNotPlant(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != fluxNamespacePath {
			t.Errorf("only the ownership read may happen: %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"metadata": {"name": "flux-system"}}`))
	}))
	got := janitorFlux(c, retractedCluster())
	if got == nil {
		t.Fatal("a declined teardown must report itself on the Cluster")
	}
	if got.Type != fluxTeardownCondition || got.Status != api.ConditionFalse {
		t.Errorf("got %+v", got)
	}
	if !strings.Contains(got.Message, "kubectl annotate namespace flux-system liken.sh/feature=flux") {
		t.Errorf("the message must name the remedy: %q", got.Message)
	}
}

func TestJanitorFluxDoesNothingWhenTheNamespaceIsAbsent(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != fluxNamespacePath {
			t.Errorf("nothing exists to tear down: %s %s", r.Method, r.URL.Path)
		}
		http.NotFound(w, r)
	}))
	if got := janitorFlux(c, retractedCluster()); got != nil {
		t.Errorf("an absent installation is not a refusal: %+v", got)
	}
}

// Stage 1: the controllers die first, and nothing else is touched on
// the same pass. This ordering is the safety property: a controller
// that processed a sync object's deletion would garbage-collect
// everything the repository ever applied.
func TestJanitorFluxKillsTheControllersFirst(t *testing.T) {
	var deleted []string
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == fluxNamespacePath:
			w.Write([]byte(plantedNamespace))
		case r.Method == http.MethodGet:
			w.Write([]byte(`{"metadata": {"name": "x"}}`))
		case r.Method == http.MethodDelete:
			deleted = append(deleted, r.URL.Path)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected call: %s %s", r.Method, r.URL.Path)
		}
	}))
	janitorFlux(c, retractedCluster())
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
		case r.Method == http.MethodGet && r.URL.Path == fluxNamespacePath:
			w.Write([]byte(plantedNamespace))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/deployments/"):
			http.NotFound(w, r)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pods"):
			w.Write([]byte(`{"items": [{"metadata": {"name": "kustomize-controller-x"}}]}`))
		default:
			t.Errorf("unexpected call: %s %s", r.Method, r.URL.Path)
		}
	}))
	if got := janitorFlux(c, retractedCluster()); got != nil {
		t.Errorf("a teardown in progress is not a refusal: %+v", got)
	}
}

// Stage 3: with the controllers provably gone, the sync objects lose
// their finalizers and everything else goes, the namespace and the
// deploy key with it.
func TestJanitorFluxTearsDownOnceControllersAreGone(t *testing.T) {
	var patched, deleted []string
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == fluxNamespacePath:
			w.Write([]byte(plantedNamespace))
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
	if got := janitorFlux(c, retractedCluster()); got != nil {
		t.Errorf("a teardown that ran has nothing to refuse: %+v", got)
	}
	if len(patched) != 2 {
		t.Errorf("both sync objects lose their finalizers, got %v", patched)
	}
	if !slices.Equal(deleted, fluxTeardownPaths) {
		t.Errorf("the teardown must cover every path, in order:\n got %v\nwant %v", deleted, fluxTeardownPaths)
	}
}

// A read that fails outright says nothing about who planted the
// installation, so the pass stops with no verdict either way.
func TestJanitorFluxStopsWhenTheOwnershipReadFails(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != fluxNamespacePath {
			t.Errorf("an unreadable namespace stops the pass: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	if got := janitorFlux(c, retractedCluster()); got != nil {
		t.Errorf("an unreadable namespace is not a refusal: %+v", got)
	}
}

// The ownership mark is a contract between two domains: the flux
// feature's own manifest declares it, and this janitor refuses to
// delete anything without it. Nothing else ties the two together, and
// a mark quietly dropped from that manifest would leave every
// retraction declining forever, which is the failure this test exists
// to catch.
func TestTheFluxNamespaceCarriesTheOwnershipMark(t *testing.T) {
	raw, err := os.ReadFile("../flux/manifests/flux-system.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var namespace struct {
		Kind     string `json:"kind"`
		Metadata struct {
			Name        string            `json:"name"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	for _, doc := range strings.Split(string(raw), "\n---\n") {
		if err := yaml.Unmarshal([]byte(doc), &namespace); err != nil {
			t.Fatalf("the feature's manifest does not parse: %v", err)
		}
		if namespace.Kind == "Namespace" && namespace.Metadata.Name == fluxNamespace {
			if got := namespace.Metadata.Annotations[featureAnnotation]; got != cluster.FeatureFlux {
				t.Errorf("%s must carry %s: %s, got %q", fluxNamespace, featureAnnotation, cluster.FeatureFlux, got)
			}
			return
		}
	}
	t.Fatalf("the flux feature's manifest declares no %s Namespace", fluxNamespace)
}
