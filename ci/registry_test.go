package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// fakeRegistry serves the parts of the distribution API that the
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
	// lag is how many reads of a manifest return 404 before the
	// registry serves it, as a registry does before a manifest that was
	// just pushed is readable.
	lag int
}

// push puts an image into the registry the way buildx pushes one with
// its attestations: an index under the tag and under its digest, one
// linux/amd64 image, an attestation manifest of the platform
// unknown/unknown, and a config with the labels.
func (f *fakeRegistry) push(pkg, tag, digest string, labels map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.manifests == nil {
		f.manifests, f.blobs = map[string]string{}, map[string]string{}
	}
	if f.tags == nil {
		f.tags = map[string][]string{}
	}
	config, _ := json.Marshal(map[string]any{"config": map[string]any{"Labels": labels}})
	manifest := fmt.Sprintf(`{"config": {"digest": %q}}`, sha256Of(string(config)))
	index := fmt.Sprintf(`{"manifests": [{"digest": %q, "platform": {"os": "linux", "architecture": "amd64"}}, {"digest": "sha256:attestation", "platform": {"os": "unknown", "architecture": "unknown"}}]}`, sha256Of(manifest))
	f.blobs[sha256Of(string(config))] = string(config)
	f.manifests[pkg+"/"+sha256Of(manifest)] = manifest
	f.manifests[pkg+"/"+digest] = index
	f.manifests[pkg+"/"+tag] = index
	f.tags[pkg] = append(f.tags[pkg], tag)
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
			if ok && f.lag > 0 {
				f.lag--
				ok = false
			}
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
		t.Fatalf("listing the tags of operator: %v", err)
	}
	if want := []string{"2026.09.26-001", "latest", "2026.09.27-001"}; !reflect.DeepEqual(tags, want) {
		t.Errorf("tags are %v, want %v", tags, want)
	}
}

func TestAPackageThatDoesNotExistHasNoTags(t *testing.T) {
	registry := (&fakeRegistry{}).serve(t)
	tags, err := registry.Tags("new-operator")
	if err != nil || tags != nil {
		t.Errorf("Tags(new-operator) = %v, %v, want no tags and no error", tags, err)
	}
}

func TestAPackageTheTokenServiceRefusesHasNoTags(t *testing.T) {
	registry := (&fakeRegistry{}).serve(t)
	tags, err := registry.Tags("missing")
	if err != nil || tags != nil {
		t.Errorf("Tags(missing) = %v, %v, want no tags and no error", tags, err)
	}
	registry.Token = "reader"
	if _, err := registry.Tags("missing"); err == nil {
		t.Error("Tags(missing) with a refused token returned no error, want an error")
	}
}

func TestAPushProbeProvesWriteAccessAndLeavesNothing(t *testing.T) {
	fake := &fakeRegistry{writers: map[string]bool{"writer": true}}
	registry := fake.serve(t)
	registry.Token = "writer"
	if err := registry.CanPush("operator"); err != nil {
		t.Fatalf("probing a push to operator: %v", err)
	}
	if want := []string{"/v2/liken-sh/operator/blobs/uploads/session"}; !reflect.DeepEqual(fake.cancelled, want) {
		t.Errorf("the cancelled uploads are %v, want %v", fake.cancelled, want)
	}
}

func TestAPushProbeReportsARefusal(t *testing.T) {
	registry := (&fakeRegistry{}).serve(t)
	registry.Token = "reader"
	if err := registry.CanPush("operator"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("CanPush(operator) with a reader token returned %v, want an error that holds 403", err)
	}
}

// imageFixture is a registry with these images. base:1 is an index of
// an amd64 image, an arm64 image, and an attestation, the way buildx
// pushes one. arm:1 is an index of an arm64 image only. base:2 is one
// image with no labels. base:3 is an index whose image manifest is
// missing.
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
				t.Errorf("Labels(%s, %s) = %v, %v, want %v", c.pkg, c.tag, labels, err, c.want)
			}
		})
	}
}

func TestATagResolvesToTheDigestOfItsManifest(t *testing.T) {
	registry := imageFixture().serve(t)
	cases := []struct {
		pkg, tag, want string
		found          bool
	}{
		{"base", "1", sha256Of(imageFixture().manifests["base/1"]), true},
		{"base", "9", "", false},
		{"missing", "1", "", false},
	}
	for _, c := range cases {
		t.Run(c.pkg+":"+c.tag, func(t *testing.T) {
			digest, found, err := registry.Digest(c.pkg, c.tag)
			if err != nil || digest != c.want || found != c.found {
				t.Errorf("Digest(%s, %s) = %q, %t, %v, want %q, %t", c.pkg, c.tag, digest, found, err, c.want, c.found)
			}
		})
	}
}

func sha256Of(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestAnIndexThatPointsAtNothingIsAnError(t *testing.T) {
	registry := imageFixture().serve(t)
	if _, err := registry.Labels("base", "3"); err == nil || !strings.Contains(err.Error(), "no such object") {
		t.Errorf("Labels(base, 3) returned %v, want an error that holds \"no such object\"", err)
	}
}
