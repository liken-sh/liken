package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"
)

func TestRunRescanNeedsALibrary(t *testing.T) {
	if err := runRescan(context.Background(), nil, rescanOptions{}, false, io.Discard); err == nil {
		t.Fatal("runRescan returned no error with no library")
	}
}

// The patch names the walk and nothing else, so a rescan leaves every fact's
// refresh time standing.
func TestRescanPatchAsksForTheWalk(t *testing.T) {
	now := time.Date(2026, 9, 19, 14, 0, 0, 0, time.UTC)

	patch, err := rescanPatch(now)
	if err != nil {
		t.Fatalf("rescanPatch: %v", err)
	}
	body := struct {
		Spec struct {
			Refresh map[string]string `json:"refresh"`
		} `json:"spec"`
	}{}
	if err := json.Unmarshal(patch, &body); err != nil {
		t.Fatalf("unmarshalling the patch: %v", err)
	}
	if stamp := now.Format(time.RFC3339Nano); body.Spec.Refresh[walkRefreshKey] != stamp {
		t.Errorf("refresh[%s] = %q, want %q", walkRefreshKey, body.Spec.Refresh[walkRefreshKey], stamp)
	}
	if len(body.Spec.Refresh) != 1 {
		t.Errorf("refresh named %d targets, want the walk alone", len(body.Spec.Refresh))
	}
}

// A matched version patches the Library with the walk's time and says
// nothing on stderr.
func TestRescanPatchesTheLibrary(t *testing.T) {
	clientset := fake.NewSimpleClientset(labeledDeployment("ghcr.io/liken-sh/library-operator:dev"))
	dyn := dynamicWith(libraryObject("media", "movies"))
	patch, err := rescanPatch(time.Now())
	if err != nil {
		t.Fatalf("rescanPatch: %v", err)
	}

	var stderr strings.Builder
	if err := patchLibraryRefresh(context.Background(), clientset, dyn, "media", "movies",
		false, patch, &stderr); err != nil {
		t.Fatalf("patchLibraryRefresh: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("a matched version wrote %q to stderr", stderr.String())
	}
	if _, held := refreshOf(t, dyn, "media", "movies")[walkRefreshKey]; !held {
		t.Fatalf("rescan did not set spec.refresh[%s]", walkRefreshKey)
	}
}
