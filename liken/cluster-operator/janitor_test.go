package main

import (
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
)

func featureDaemonSet(name, slug string) annotatedObject {
	w := annotatedObject{}
	w.Metadata.Name = name
	if slug != "" {
		w.Metadata.Annotations = map[string]string{featureAnnotation: slug}
	}
	return w
}

func retractionNames(workloads []annotatedObject) []string {
	names := make([]string, 0, len(workloads))
	for _, w := range workloads {
		names = append(names, w.Metadata.Name)
	}
	return names
}

func TestDecideRetractionsLeavesDeclaredFeatureWorkloads(t *testing.T) {
	features := map[string]*cluster.FeatureConfig{"iscsi": {}}
	workloads := []annotatedObject{featureDaemonSet("liken-iscsid", "iscsi")}
	if got := decideRetractions(features, workloads); len(got) != 0 {
		t.Fatalf("retractions are %v, want none", retractionNames(got))
	}
}

func TestDecideRetractionsReturnsRetractedFeatureWorkloads(t *testing.T) {
	workloads := []annotatedObject{featureDaemonSet("liken-iscsid", "iscsi")}
	got := decideRetractions(nil, workloads)
	if names := retractionNames(got); len(names) != 1 || names[0] != "liken-iscsid" {
		t.Fatalf("retractions are %v, want [liken-iscsid]", names)
	}
}

func TestDecideRetractionsChecksEachWorkloadByItsFeature(t *testing.T) {
	features := map[string]*cluster.FeatureConfig{"nfs": {}}
	workloads := []annotatedObject{
		featureDaemonSet("liken-iscsid", "iscsi"),
		featureDaemonSet("liken-nfs-helper", "nfs"),
	}
	got := decideRetractions(features, workloads)
	if names := retractionNames(got); len(names) != 1 || names[0] != "liken-iscsid" {
		t.Fatalf("retractions are %v, want [liken-iscsid]", names)
	}
}

func TestDecideRetractionsIgnoresWorkloadsWithoutTheAnnotation(t *testing.T) {
	workloads := []annotatedObject{featureDaemonSet("liken-machine-operator", "")}
	if got := decideRetractions(nil, workloads); len(got) != 0 {
		t.Fatalf("retractions are %v, want none", retractionNames(got))
	}
}

func TestDeleteRetractedWorkloadsDeletesThroughTheAPI(t *testing.T) {
	var deletes []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/daemonsets"):
			list := struct {
				Items []annotatedObject `json:"items"`
			}{Items: []annotatedObject{
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
	deleteRetractedWorkloads(&fleetReader{client: testClient(t, handler)}, clusterDoc)
	want := "/apis/apps/v1/namespaces/liken-system/daemonsets/liken-iscsid"
	if len(deletes) != 1 || deletes[0] != want {
		t.Fatalf("deleted %v, want exactly [%s]", deletes, want)
	}
}

// retractedCluster declares no features, so flux is retracted.
func retractedCluster() *cluster.Cluster {
	c := &cluster.Cluster{}
	c.Metadata.Name = "lab"
	return c
}

// annotatedFluxNamespace is the flux-system Namespace that liken's
// flux manifest creates, with the feature annotation.
const annotatedFluxNamespace = `{"metadata": {"name": "flux-system",
	"annotations": {"liken.sh/feature": "flux"}}}`

func TestTearDownFluxLeavesADeclaredFeatureAlone(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected API call: %s %s", r.Method, r.URL.Path)
	}))
	if got := tearDownFlux(c, fluxCluster()); got != nil {
		t.Errorf("the condition is %+v, want nil for a declared feature", got)
	}
}

// An adopted cluster can run its own Flux while its document does not
// declare the feature.
func TestTearDownFluxDeletesNothingLikenDidNotCreate(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != fluxNamespacePath {
			t.Errorf("unexpected API call: %s %s, want only the read of the Namespace", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"metadata": {"name": "flux-system"}}`))
	}))
	got := tearDownFlux(c, retractedCluster())
	if got == nil {
		t.Fatal("the condition is nil, want a condition for the declined teardown")
	}
	if got.Type != fluxTeardownCondition || got.Status != api.ConditionFalse {
		t.Errorf("the condition is %+v, want type %s with status False", got, fluxTeardownCondition)
	}
	if !strings.Contains(got.Message, "kubectl annotate namespace flux-system liken.sh/feature=flux") {
		t.Errorf("the message is %q, want the kubectl annotate command", got.Message)
	}
}

func TestTearDownFluxDoesNothingWhenTheNamespaceIsAbsent(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != fluxNamespacePath {
			t.Errorf("unexpected API call: %s %s, want none because no installation exists", r.Method, r.URL.Path)
		}
		http.NotFound(w, r)
	}))
	if got := tearDownFlux(c, retractedCluster()); got != nil {
		t.Errorf("the condition is %+v, want nil when no installation exists", got)
	}
}

// A controller that runs when a sync object is deleted prunes
// everything the repository applied.
func TestTearDownFluxDeletesTheControllersFirst(t *testing.T) {
	var deleted []string
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == fluxNamespacePath:
			w.Write([]byte(annotatedFluxNamespace))
		case r.Method == http.MethodGet:
			w.Write([]byte(`{"metadata": {"name": "x"}}`))
		case r.Method == http.MethodDelete:
			deleted = append(deleted, r.URL.Path)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected API call: %s %s", r.Method, r.URL.Path)
		}
	}))
	tearDownFlux(c, retractedCluster())
	want := []string{
		"/apis/apps/v1/namespaces/flux-system/deployments/source-controller",
		"/apis/apps/v1/namespaces/flux-system/deployments/kustomize-controller",
	}
	if !slices.Equal(deleted, want) {
		t.Errorf("deleted %v, want exactly the two Deployments", deleted)
	}
}

// The fake pod list returns no pods, as a real one can during a
// rollout, so only the failed delete keeps the pass from stage 3.
func TestTearDownFluxStopsWhenAControllerDeleteFails(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == fluxNamespacePath:
			w.Write([]byte(annotatedFluxNamespace))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/deployments/"):
			w.Write([]byte(`{"metadata": {"name": "x"}}`))
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/deployments/"):
			w.WriteHeader(http.StatusInternalServerError)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pods"):
			w.Write([]byte(`{"items": []}`))
		default:
			t.Errorf("unexpected API call: %s %s, want none after a failed Deployment delete", r.Method, r.URL.Path)
		}
	}))
	tearDownFlux(c, retractedCluster())
}

func TestTearDownFluxWaitsForTerminatingControllers(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == fluxNamespacePath:
			w.Write([]byte(annotatedFluxNamespace))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/deployments/"):
			http.NotFound(w, r)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pods"):
			w.Write([]byte(`{"items": [{"metadata": {"name": "kustomize-controller-x"}}]}`))
		default:
			t.Errorf("unexpected API call: %s %s", r.Method, r.URL.Path)
		}
	}))
	if got := tearDownFlux(c, retractedCluster()); got != nil {
		t.Errorf("the condition is %+v, want nil while the teardown runs", got)
	}
}

func TestTearDownFluxDeletesEverythingOnceTheControllersAreGone(t *testing.T) {
	var patched, deleted []string
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == fluxNamespacePath:
			w.Write([]byte(annotatedFluxNamespace))
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
			t.Errorf("unexpected API call: %s %s", r.Method, r.URL.Path)
		}
	}))
	if got := tearDownFlux(c, retractedCluster()); got != nil {
		t.Errorf("the condition is %+v, want nil after the teardown ran", got)
	}
	if len(patched) != 2 {
		t.Errorf("patched %v, want both sync objects to lose their finalizers", patched)
	}
	if !slices.Equal(deleted, fluxTeardownPaths) {
		t.Errorf("deleted paths:\n got %v\nwant %v", deleted, fluxTeardownPaths)
	}
}

func TestTearDownFluxStopsWhenTheNamespaceReadFails(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != fluxNamespacePath {
			t.Errorf("unexpected API call: %s %s, want none after a failed read of the Namespace", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	if got := tearDownFlux(c, retractedCluster()); got != nil {
		t.Errorf("the condition is %+v, want nil when the Namespace cannot be read", got)
	}
}

// Without the annotation in the flux feature's manifest, tearDownFlux
// refuses every teardown, and no other test connects the two files.
func TestTheFluxManifestAnnotatesItsNamespace(t *testing.T) {
	raw, err := os.ReadFile("../flux/manifests/flux-system.yaml")
	if err != nil {
		t.Fatalf("reading the flux feature manifest: %v", err)
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
			t.Fatalf("parsing the flux feature manifest: %v", err)
		}
		if namespace.Kind == "Namespace" && namespace.Metadata.Name == fluxNamespace {
			if got := namespace.Metadata.Annotations[featureAnnotation]; got != cluster.FeatureFlux {
				t.Errorf("the %s Namespace has annotation %s %q, want %q", fluxNamespace, featureAnnotation, got, cluster.FeatureFlux)
			}
			return
		}
	}
	t.Fatalf("the flux feature manifest has no %s Namespace", fluxNamespace)
}
