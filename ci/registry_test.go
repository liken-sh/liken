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
// program uses: the token service, a paged tag list, manifests and
// blobs, and blob upload sessions. A token grants a push only for a
// credential in writers. manifests holds each manifest by
// <package>/<tag or digest>, and blobs holds each blob by its digest.
type fakeRegistry struct {
	mu        sync.Mutex
	tags      map[string][]string
	writers   map[string]bool
	manifests map[string]string
	blobs     map[string]string
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
		case strings.Contains(r.URL.Path, "/manifests/") && r.Method == http.MethodGet:
			pkg, reference, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v2/liken-sh/"), "/manifests/")
			manifest, ok := f.manifests[pkg+"/"+reference]
			if !ok || !strings.Contains(r.Header.Get("Accept"), "application/vnd.oci.image.index.v1+json") {
				http.NotFound(w, r)
				return
			}
			w.Write([]byte(manifest))
		case strings.Contains(r.URL.Path, "/blobs/sha256:") && r.Method == http.MethodGet:
			blob, ok := f.blobs[r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Write([]byte(blob))
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

// imageFixture is a registry that holds base:1 as an index of an amd64
// image, an arm64 image, and an attestation, the way buildx pushes
// one; base:2 as one image with no labels; and base:3 as an index
// whose image manifest is missing.
func imageFixture() *fakeRegistry {
	return &fakeRegistry{
		manifests: map[string]string{
			"base/1": `{"manifests": [
				{"digest": "sha256:arm", "platform": {"os": "linux", "architecture": "arm64"}},
				{"digest": "sha256:amd", "platform": {"os": "linux", "architecture": "amd64"}},
				{"digest": "sha256:att", "platform": {"os": "unknown", "architecture": "unknown"}}]}`,
			"base/sha256:amd": `{"config": {"digest": "sha256:amdconfig"}}`,
			"base/sha256:arm": `{"config": {"digest": "sha256:armconfig"}}`,
			"base/2":          `{"config": {"digest": "sha256:plain"}}`,
			"base/3":          `{"manifests": [{"digest": "sha256:gone", "platform": {"os": "linux", "architecture": "amd64"}}]}`,
			"arm/1":           `{"manifests": [{"digest": "sha256:arm", "platform": {"os": "linux", "architecture": "arm64"}}]}`,
			"arm/sha256:arm":  `{"config": {"digest": "sha256:armconfig"}}`,
		},
		blobs: map[string]string{
			"sha256:amdconfig": `{"config": {"Labels": {"sh.liken.recipe": "sha256:amd"}}}`,
			"sha256:armconfig": `{"config": {"Labels": {"sh.liken.recipe": "sha256:arm"}}}`,
			"sha256:plain":     `{"config": {}}`,
		},
	}
}

func TestTheLabelsOfAnImageAreRead(t *testing.T) {
	registry := imageFixture().serve(t)
	cases := []struct {
		pkg, tag string
		want     map[string]string
	}{
		{"base", "1", map[string]string{"sh.liken.recipe": "sha256:amd"}},
		{"arm", "1", map[string]string{"sh.liken.recipe": "sha256:arm"}},
		{"base", "2", map[string]string{}},
		{"base", "9", nil},
		{"missing", "1", nil},
	}
	for _, c := range cases {
		t.Run(c.pkg+":"+c.tag, func(t *testing.T) {
			labels, err := registry.Labels(c.pkg, c.tag)
			if err != nil || !reflect.DeepEqual(labels, c.want) {
				t.Errorf("labels %v, err %v", labels, err)
			}
		})
	}
}

func TestAnIndexThatPointsAtNothingIsAnError(t *testing.T) {
	registry := imageFixture().serve(t)
	if _, err := registry.Labels("base", "3"); err == nil || !strings.Contains(err.Error(), "no such object") {
		t.Errorf("got %v", err)
	}
}
