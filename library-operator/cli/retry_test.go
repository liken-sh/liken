package main

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRetryFacts(t *testing.T) {
	cases := []struct {
		name string
		only string
		want []string
	}{
		{"no --only names every fact", "", retryFactVocabulary},
		{"one fact", "trickplay", []string{"trickplay"}},
		{"the worker fact refresh leaves out", "appearances", []string{"appearances"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := retryFacts(tc.only)
			if err != nil {
				t.Fatalf("retryFacts(%q): %v", tc.only, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("retryFacts(%q) = %v, want %v", tc.only, got, tc.want)
			}
		})
	}
}

func TestRetryRefusesWhatSpecRetryRefuses(t *testing.T) {
	for _, only := range []string{"bogus", "scan"} {
		if _, err := retryFacts(only); err == nil {
			t.Fatalf("retryFacts(%q) returned no error", only)
		}
	}
}

func TestRetryPatchSetsOnlySpecRetry(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	patch, err := retryPatch("trickplay", now)
	if err != nil {
		t.Fatalf("retryPatch: %v", err)
	}
	var body struct {
		Spec map[string]map[string]string `json:"spec"`
	}
	if err := json.Unmarshal(patch, &body); err != nil {
		t.Fatalf("unmarshalling the patch: %v", err)
	}
	want := map[string]map[string]string{"retry": {"trickplay": now.Format(time.RFC3339Nano)}}
	if len(body.Spec) != 1 || len(body.Spec["retry"]) != 1 || body.Spec["retry"]["trickplay"] != want["retry"]["trickplay"] {
		t.Fatalf("patch spec = %v, want %v", body.Spec, want)
	}
}

func retryOf(t *testing.T, dyn *dynamicfake.FakeDynamicClient, namespace, name string) map[string]any {
	t.Helper()
	got, err := dyn.Resource(libraryResource).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the Library back: %v", err)
	}
	retry, _, err := unstructured.NestedMap(got.Object, "spec", "retry")
	if err != nil {
		t.Fatalf("reading spec.retry: %v", err)
	}
	return retry
}

func TestRetryPatchesTheLibraryAndLeavesRefresh(t *testing.T) {
	clientset := fake.NewSimpleClientset(labeledDeployment("ghcr.io/liken-sh/library-operator:dev"))
	dyn := dynamicWith(libraryObject("media", "movies"))
	patch, err := retryPatch("trickplay", time.Now())
	if err != nil {
		t.Fatalf("retryPatch: %v", err)
	}

	var stderr strings.Builder
	if err := patchLibraryRefresh(context.Background(), clientset, dyn, "media", "movies", false, patch, &stderr); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, held := retryOf(t, dyn, "media", "movies")["trickplay"]; !held {
		t.Fatal("retry did not set spec.retry[trickplay]")
	}
	if refresh := refreshOf(t, dyn, "media", "movies"); len(refresh) != 0 {
		t.Fatalf("retry wrote spec.refresh %v", refresh)
	}
}

func TestRunRetryRejectsBadOptionsBeforeTheCluster(t *testing.T) {
	if err := runRetry(context.Background(), nil, retryOptions{}, io.Discard); err == nil {
		t.Fatal("runRetry reached the cluster with no library")
	}
	if err := runRetry(context.Background(), nil, retryOptions{Name: "movies", Only: "scan"}, io.Discard); err == nil {
		t.Fatal("runRetry reached the cluster with a target spec.retry refuses")
	}
}
