package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// fakeRegistry answers the parts of the distribution API that the
// program uses: the token service, a paged tag list, and blob upload
// sessions. A token grants a push only for a credential in writers.
type fakeRegistry struct {
	mu        sync.Mutex
	tags      map[string][]string
	writers   map[string]bool
	cancelled []string
}

func (f *fakeRegistry) serve(t *testing.T) Registry {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == "/token":
			_, password, _ := r.BasicAuth()
			if strings.Contains(r.URL.Query().Get("scope"), "/missing:") {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			scope := r.URL.Query().Get("scope")
			token := "pull"
			if strings.HasSuffix(scope, ":pull,push") && f.writers[password] {
				token = "push"
			}
			json.NewEncoder(w).Encode(map[string]string{"token": token})
		case strings.HasSuffix(r.URL.Path, "/tags/list"):
			pkg := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v2/liken-sh/"), "/tags/list")
			tags, ok := f.tags[pkg]
			if !ok {
				http.NotFound(w, r)
				return
			}
			// One tag per page, so every test walks the Link header.
			start := 0
			for i, tag := range tags {
				if tag == r.URL.Query().Get("last") {
					start = i + 1
				}
			}
			page := tags[start:min(start+1, len(tags))]
			if start+1 < len(tags) {
				w.Header().Set("Link", fmt.Sprintf(`</v2/liken-sh/%s/tags/list?last=%s&n=1>; rel="next"`, pkg, page[0]))
			}
			json.NewEncoder(w).Encode(map[string]any{"tags": page})
		case strings.HasSuffix(r.URL.Path, "/blobs/uploads/") && r.Method == http.MethodPost:
			if r.Header.Get("Authorization") != "Bearer push" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Header().Set("Location", r.URL.Path+"session")
			w.WriteHeader(http.StatusAccepted)
		case strings.HasSuffix(r.URL.Path, "/blobs/uploads/session") && r.Method == http.MethodDelete:
			f.cancelled = append(f.cancelled, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return Registry{Base: server.URL, Owner: "liken-sh", Client: server.Client()}
}

func TestTheTagsOfAPackageAreReadAcrossPages(t *testing.T) {
	fake := &fakeRegistry{tags: map[string][]string{"operator": {"2026.09.26-001", "latest", "2026.09.27-001"}}}
	registry := fake.serve(t)
	tags, err := registry.Tags("operator")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"2026.09.26-001", "latest", "2026.09.27-001"}; !reflect.DeepEqual(tags, want) {
		t.Errorf("tags %v", tags)
	}
}

func TestAPackageThatDoesNotExistHasNoTags(t *testing.T) {
	registry := (&fakeRegistry{}).serve(t)
	tags, err := registry.Tags("new-operator")
	if err != nil || tags != nil {
		t.Errorf("tags %v, err %v", tags, err)
	}
}

func TestAPackageTheTokenServiceRefusesHasNoTags(t *testing.T) {
	registry := (&fakeRegistry{}).serve(t)
	tags, err := registry.Tags("missing")
	if err != nil || tags != nil {
		t.Errorf("tags %v, err %v", tags, err)
	}
	registry.Token = "reader"
	if _, err := registry.Tags("missing"); err == nil {
		t.Error("a refusal of a credential read as no tags")
	}
}

func TestAPushProbeProvesWriteAccessAndLeavesNothing(t *testing.T) {
	fake := &fakeRegistry{writers: map[string]bool{"writer": true}}
	registry := fake.serve(t)
	registry.Token = "writer"
	if err := registry.CanPush("operator"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"/v2/liken-sh/operator/blobs/uploads/session"}; !reflect.DeepEqual(fake.cancelled, want) {
		t.Errorf("cancelled %v", fake.cancelled)
	}
}

func TestAPushProbeReportsARefusal(t *testing.T) {
	registry := (&fakeRegistry{}).serve(t)
	registry.Token = "reader"
	if err := registry.CanPush("operator"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("got %v", err)
	}
}
