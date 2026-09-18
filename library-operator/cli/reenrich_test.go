package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRefreshFacts(t *testing.T) {
	all, err := refreshFacts("")
	if err != nil {
		t.Fatalf("refreshFacts(all): %v", err)
	}
	if len(all) != len(refreshFactVocabulary) {
		t.Fatalf("refreshFacts(all) named %d facts, want %d", len(all), len(refreshFactVocabulary))
	}

	one, err := refreshFacts("poster")
	if err != nil {
		t.Fatalf("refreshFacts(poster): %v", err)
	}
	if len(one) != 1 || one[0] != "poster" {
		t.Fatalf("refreshFacts(poster) = %v, want [poster]", one)
	}

	if _, err := refreshFacts("bogus"); err == nil {
		t.Fatal("refreshFacts returned no error for an unknown fact")
	}
}

func TestRefreshPatch(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	patch, err := refreshPatch("poster", now)
	if err != nil {
		t.Fatalf("refreshPatch: %v", err)
	}
	var body struct {
		Spec struct {
			Refresh map[string]string `json:"refresh"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(patch, &body); err != nil {
		t.Fatalf("unmarshalling the patch: %v", err)
	}
	stamp := now.Format(time.RFC3339Nano)
	if body.Spec.Refresh["poster"] != stamp {
		t.Fatalf("refresh[poster] = %q, want %q", body.Spec.Refresh["poster"], stamp)
	}
	if len(body.Spec.Refresh) != 1 {
		t.Fatalf("refresh named %d facts, want 1", len(body.Spec.Refresh))
	}

	if _, err := refreshPatch("bogus", now); err == nil {
		t.Fatal("refreshPatch returned no error for an unknown fact")
	}
}

func libraryObject(namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "library.liken.sh/v1alpha1",
			"kind":       "Library",
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]any{"kind": "movies"},
		},
	}
}

func dynamicWith(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{libraryResource: "LibraryList"},
		objects...)
}

func refreshOf(t *testing.T, dyn *dynamicfake.FakeDynamicClient, namespace, name string) map[string]any {
	t.Helper()
	got, err := dyn.Resource(libraryResource).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the Library back: %v", err)
	}
	refresh, _, err := unstructured.NestedMap(got.Object, "spec", "refresh")
	if err != nil {
		t.Fatalf("reading spec.refresh: %v", err)
	}
	return refresh
}

func TestReenrichPatchesTheLibrary(t *testing.T) {
	clientset := fake.NewSimpleClientset(labeledDeployment("ghcr.io/liken-sh/library-operator:dev"))
	dyn := dynamicWith(libraryObject("media", "movies"))
	patch, err := refreshPatch("poster", time.Now())
	if err != nil {
		t.Fatalf("refreshPatch: %v", err)
	}

	var stderr strings.Builder
	if err := reenrich(context.Background(), clientset, dyn, "media", "movies", false, patch, &stderr); err != nil {
		t.Fatalf("reenrich: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("a matched version wrote %q to stderr", stderr.String())
	}
	if _, held := refreshOf(t, dyn, "media", "movies")["poster"]; !held {
		t.Fatal("reenrich did not set spec.refresh[poster]")
	}
}

func TestReenrichWarnsOnDrift(t *testing.T) {
	clientset := fake.NewSimpleClientset(labeledDeployment("ghcr.io/liken-sh/library-operator:2026.09.03-007"))
	dyn := dynamicWith(libraryObject("media", "movies"))
	patch, err := refreshPatch("", time.Now())
	if err != nil {
		t.Fatalf("refreshPatch: %v", err)
	}

	var stderr strings.Builder
	if err := reenrich(context.Background(), clientset, dyn, "media", "movies", false, patch, &stderr); err != nil {
		t.Fatalf("reenrich: %v", err)
	}
	if !strings.Contains(stderr.String(), syncCommand) {
		t.Fatalf("stderr = %q, want the sync command", stderr.String())
	}
	if len(refreshOf(t, dyn, "media", "movies")) != len(refreshFactVocabulary) {
		t.Fatal("reenrich did not set every fact")
	}
}

func TestReenrichForceSilencesTheWarning(t *testing.T) {
	clientset := fake.NewSimpleClientset(labeledDeployment("ghcr.io/liken-sh/library-operator:2026.09.03-007"))
	dyn := dynamicWith(libraryObject("media", "movies"))
	patch, err := refreshPatch("poster", time.Now())
	if err != nil {
		t.Fatalf("refreshPatch: %v", err)
	}

	var stderr strings.Builder
	if err := reenrich(context.Background(), clientset, dyn, "media", "movies", true, patch, &stderr); err != nil {
		t.Fatalf("reenrich: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("--force wrote %q to stderr", stderr.String())
	}
}

func TestReenrichWithoutADeploymentStillPatches(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	dyn := dynamicWith(libraryObject("media", "movies"))
	patch, err := refreshPatch("poster", time.Now())
	if err != nil {
		t.Fatalf("refreshPatch: %v", err)
	}

	var stderr strings.Builder
	if err := reenrich(context.Background(), clientset, dyn, "media", "movies", false, patch, &stderr); err != nil {
		t.Fatalf("reenrich: %v", err)
	}
	if !strings.Contains(stderr.String(), "operator version") {
		t.Fatalf("stderr = %q, want the version-read message", stderr.String())
	}
	if _, held := refreshOf(t, dyn, "media", "movies")["poster"]; !held {
		t.Fatal("reenrich did not set spec.refresh[poster]")
	}
}

func TestReenrichSurfacesAMissingLibrary(t *testing.T) {
	clientset := fake.NewSimpleClientset(labeledDeployment("ghcr.io/liken-sh/library-operator:dev"))
	dyn := dynamicWith()
	patch, err := refreshPatch("poster", time.Now())
	if err != nil {
		t.Fatalf("refreshPatch: %v", err)
	}
	if err := reenrich(context.Background(), clientset, dyn, "media", "ghost", false, patch, io.Discard); err == nil {
		t.Fatal("reenrich returned no error for a Library that is not there")
	}
}

func TestRunReenrichRejectsBadOptionsBeforeTheCluster(t *testing.T) {
	if err := runReenrich(context.Background(), nil, reenrichOptions{}, io.Discard); err == nil {
		t.Fatal("runReenrich reached the cluster with no library")
	}
	if err := runReenrich(context.Background(), nil, reenrichOptions{Name: "movies", Only: "bogus"}, io.Discard); err == nil {
		t.Fatal("runReenrich reached the cluster with an unknown fact")
	}
}
