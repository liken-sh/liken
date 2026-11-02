// Package verifytest serves real published files of `liken`'s CI to a
// test: a release on a channel, and an image with its attestations and
// its signature in a registry. Both run in the test's process, so a
// test of verify, or of a command that runs verify, reaches no network.
//
// Each fixture is what the channel or ghcr.io served for release
// 2026.11.01-001, byte for byte, with the Sigstore bundles that the
// publish jobs wrote. A bundle verifies only over the bytes it signed,
// so a test can change a file to prove that a check refuses it, and
// cannot sign a file of its own.
package verifytest

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

//go:embed testdata
var testdata embed.FS

// Version is the release that the fixtures come from.
const Version = "2026.11.01-001"

// IndexDigest is the digest of the index of
// ghcr.io/liken-sh/bluetoothd:2026.11.01-001, which its signature
// covers.
const IndexDigest = "sha256:abbc97362c34e58f7882f1f97512b7bcce4ed90cfc3765dfba46506bf84ea979"

// ReleaseFiles are the files of the release that the fixtures hold.
// The channel holds eight more artifacts, the system image among them,
// which are too large for the repository, so a fixture channel answers
// 404 for them.
var ReleaseFiles = []string{"release.yaml", "release.yaml.sigstore.json", "sbom.cdx.json", "sbom.cdx.json.sigstore.json"}

// ReleaseFile reads one published file of the release. The SBOM is
// stored compressed, because it is larger than the repository takes as
// one file, and ReleaseFile returns the bytes that the channel serves.
func ReleaseFile(t testing.TB, name string) []byte {
	t.Helper()
	file := path.Join("testdata", Version, name)
	if data, err := testdata.ReadFile(file); err == nil {
		return data
	}
	f, err := testdata.Open(file + ".gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Published returns the release's files by name, for a test to change
// before it serves them.
func Published(t testing.TB) map[string][]byte {
	files := map[string][]byte{}
	for _, name := range ReleaseFiles {
		files[name] = ReleaseFile(t, name)
	}
	return files
}

// ServeChannel serves the files as the release at version, the way a
// channel lays out a release under its version, and returns the
// channel's base URL.
func ServeChannel(t testing.TB, version string, files map[string][]byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for name, data := range files {
		mux.HandleFunc("GET /"+version+"/"+name, func(w http.ResponseWriter, _ *http.Request) {
			w.Write(data)
		})
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// Tampered returns the bytes with one byte changed.
func Tampered(data []byte) []byte {
	out := bytes.Clone(data)
	out[len(out)/2] ^= 0x01
	return out
}

// ImageFile reads one file of the image's fixtures: the index, the
// amd64 image's manifest, the attestation manifest and its SPDX
// statement, cosign's signature manifest and its bundle, and the index
// that ghcr.io serves under the tag sha256-<hex> to list the referrers
// of the index. The image's layers and the provenance statement are
// not here, because a check reads neither.
func ImageFile(t testing.TB, name string) []byte {
	t.Helper()
	data, err := testdata.ReadFile(path.Join("testdata", "bluetoothd", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// rawManifest pushes manifest bytes unchanged, so the registry serves
// them at the digest that ghcr.io serves them at.
type rawManifest []byte

func (m rawManifest) RawManifest() ([]byte, error) { return m, nil }

func (m rawManifest) MediaType() (types.MediaType, error) {
	var head struct {
		MediaType types.MediaType `json:"mediaType"`
	}
	return head.MediaType, json.Unmarshal(m, &head)
}

// A Registry is an in-memory registry, and the repository of the
// image in it.
type Registry struct {
	Repo name.Repository
}

// NewRegistry starts an in-memory registry. ghcr.io has no referrers
// API, so cosign lists the signatures of a digest in an index under the
// tag sha256-<hex>, and go-containerregistry reads them from there. The
// in-memory registry has no referrers API either, unless a caller asks
// for one, and its API reports a manifest's config media type as the
// artifact type, where the OCI specification takes the manifest's own
// artifactType first. So the registry lists signatures under the tag,
// the form that ghcr.io serves.
func NewRegistry(t testing.TB) Registry {
	t.Helper()
	server := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(server.Close)
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := name.NewRepository(u.Host + "/liken-sh/bluetoothd")
	if err != nil {
		t.Fatal(err)
	}
	return Registry{Repo: repo}
}

// Blob pushes a blob.
func (r Registry) Blob(t testing.TB, data []byte) {
	t.Helper()
	if err := remote.WriteLayer(r.Repo, static.NewLayer(data, types.MediaType("application/octet-stream"))); err != nil {
		t.Fatal(err)
	}
}

// Manifest pushes manifest bytes under a tag, or under their own digest
// when the tag is empty, and returns their digest.
func (r Registry) Manifest(t testing.TB, data []byte, tag string) v1.Hash {
	t.Helper()
	hash, _, err := v1.SHA256(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	var ref name.Reference = r.Repo.Digest(hash.String())
	if tag != "" {
		ref = r.Repo.Tag(tag)
	}
	if err := remote.Put(ref, rawManifest(data)); err != nil {
		t.Fatal(err)
	}
	return hash
}

// Publish pushes the image and its attestations the way buildx does,
// with the index under the release's tag.
func (r Registry) Publish(t testing.TB) {
	r.Blob(t, ImageFile(t, "sbom.intoto.json"))
	r.Manifest(t, ImageFile(t, "image.json"), "")
	r.Manifest(t, ImageFile(t, "attestation.json"), "")
	r.Manifest(t, ImageFile(t, "index.json"), Version)
}

// Sign pushes a signature manifest that holds the bundle as its one
// layer, and lists it under the tag of the referrers tag schema, as
// cosign does on ghcr.io. With the published bundle, every byte is the
// one that ghcr.io serves.
func (r Registry) Sign(t testing.TB, bundleJSON []byte) {
	t.Helper()
	r.Blob(t, bundleJSON)
	signature, referrers := ImageFile(t, "signature.json"), ImageFile(t, "referrers.json")
	if !bytes.Equal(bundleJSON, ImageFile(t, "signature.sigstore.json")) {
		signature = withLayer(t, signature, bundleJSON)
		referrers = listing(t, signature)
	}
	r.Manifest(t, signature, "")
	r.Manifest(t, referrers, strings.Replace(IndexDigest, ":", "-", 1))
}

// withLayer returns the signature manifest with its layer swapped for
// another bundle.
func withLayer(t testing.TB, manifest, layer []byte) []byte {
	m, err := v1.ParseManifest(bytes.NewReader(manifest))
	if err != nil {
		t.Fatal(err)
	}
	hash, size, err := v1.SHA256(bytes.NewReader(layer))
	if err != nil {
		t.Fatal(err)
	}
	m.Layers[0].Digest, m.Layers[0].Size = hash, size
	return marshal(t, m)
}

// listing is the index that lists one signature manifest under the tag
// sha256-<hex>.
func listing(t testing.TB, signature []byte) []byte {
	hash, size, err := v1.SHA256(bytes.NewReader(signature))
	if err != nil {
		t.Fatal(err)
	}
	return marshal(t, v1.IndexManifest{
		SchemaVersion: 2,
		MediaType:     types.OCIImageIndex,
		Manifests: []v1.Descriptor{{
			MediaType: types.OCIManifestSchema1, Size: size, Digest: hash,
			ArtifactType: "application/vnd.dev.sigstore.bundle.v0.3+json",
		}},
	})
}

func marshal(t testing.TB, v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
