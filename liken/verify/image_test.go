package verify

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/liken-sh/liken/liken/verify/verifytest"
)

func image(t *testing.T, name string) []byte { return verifytest.ImageFile(t, name) }

func TestImageAcceptsThePublishedSignature(t *testing.T) {
	r := verifytest.NewRegistry(t)
	r.Publish(t)
	r.Sign(t, image(t, "signature.sigstore.json"))

	report, err := Image(t.Context(), r.Repo.Tag(verifytest.Version).String())
	if err != nil {
		t.Fatal(err)
	}
	if want := r.Repo.Digest(verifytest.IndexDigest).String(); report.Reference != want {
		t.Errorf("the reference is %s, want %s", report.Reference, want)
	}
	if want := "https://github.com/liken-sh/liken/.github/workflows/component-bluetooth-operator.yaml@refs/tags/2026.11.01-001"; report.Signer.Workflow != want {
		t.Errorf("the signer is %s, want %s", report.Signer.Workflow, want)
	}
	want := []Attestation{{Platform: "linux/amd64", Packages: 21, Provenance: "https://slsa.dev/provenance/v1"}}
	for i := range report.Attestations {
		report.Attestations[i].SBOM = nil
	}
	if !reflect.DeepEqual(report.Attestations, want) {
		t.Errorf("the attestations are %+v, want %+v", report.Attestations, want)
	}
}

func TestImageRefuses(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, r verifytest.Registry)
		ref    string
		reason string
	}{
		{"an image with no signature", func(t *testing.T, r verifytest.Registry) { r.Publish(t) }, verifytest.Version,
			"carries no Sigstore signature"},
		{"a signature of another file", func(t *testing.T, r verifytest.Registry) {
			r.Publish(t)
			r.Sign(t, release(t, "release.yaml.sigstore.json"))
		}, verifytest.Version, "no signature of"},
		{"a tag that names one platform's manifest", func(t *testing.T, r verifytest.Registry) {
			r.Manifest(t, image(t, "image.json"), "amd64")
		}, "amd64", "a published image is an index"},
		{"a tag that does not exist", func(*testing.T, verifytest.Registry) {}, verifytest.Version, "reading"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := verifytest.NewRegistry(t)
			c.setup(t, r)
			_, err := Image(t.Context(), r.Repo.Tag(c.ref).String())
			if err == nil || !strings.Contains(err.Error(), c.reason) {
				t.Errorf("the check returned %v, want an error that says %q", err, c.reason)
			}
		})
	}
}

// The signature covers the index, so an index that differs from the
// published one fails the signature before its attestations are read.
// These cases read the attestations of a changed index directly.
func TestAttestationsRefuse(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, r verifytest.Registry, index *v1.IndexManifest)
		reason string
	}{
		{"an image with no attestation manifest", func(_ *testing.T, _ verifytest.Registry, index *v1.IndexManifest) {
			index.Manifests = index.Manifests[:1]
		}, "the linux/amd64 image carries no attestations"},
		{"an index with no image", func(_ *testing.T, _ verifytest.Registry, index *v1.IndexManifest) {
			index.Manifests = index.Manifests[1:]
		}, "the index names no image"},
		{"an attestation with no provenance", func(t *testing.T, r verifytest.Registry, index *v1.IndexManifest) {
			index.Manifests[1].Digest = attestationWith(t, r, func(m *v1.Manifest) { m.Layers = m.Layers[:1] })
		}, "carries no SLSA provenance"},
		{"an attestation with no SBOM", func(t *testing.T, r verifytest.Registry, index *v1.IndexManifest) {
			index.Manifests[1].Digest = attestationWith(t, r, func(m *v1.Manifest) { m.Layers = m.Layers[1:] })
		}, "carries no SPDX SBOM"},
		{"an SBOM of another image", func(_ *testing.T, _ verifytest.Registry, index *v1.IndexManifest) {
			index.Manifests[0].Digest, index.Manifests[1].Annotations[referenceDigest] = index.Manifests[1].Digest, index.Manifests[1].Digest.String()
			index.Manifests[0].Platform = &v1.Platform{OS: "linux", Architecture: "arm64"}
		}, "the SBOM of the linux/arm64 image describes another image"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := verifytest.NewRegistry(t)
			r.Publish(t)
			index, err := v1.ParseIndexManifest(bytes.NewReader(image(t, "index.json")))
			if err != nil {
				t.Fatal(err)
			}
			c.change(t, r, index)
			_, err = attestations(r.Repo, index, nil)
			if err == nil || !strings.Contains(err.Error(), c.reason) {
				t.Errorf("the check returned %v, want an error that says %q", err, c.reason)
			}
		})
	}
}

// attestationWith pushes a changed copy of the attestation manifest and
// returns its digest.
func attestationWith(t *testing.T, r verifytest.Registry, change func(*v1.Manifest)) v1.Hash {
	m, err := v1.ParseManifest(bytes.NewReader(image(t, "attestation.json")))
	if err != nil {
		t.Fatal(err)
	}
	change(m)
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return r.Manifest(t, data, "")
}

// buildx names an image in the SBOM's statement once for each name the
// build published it under, so an image with two names has two
// subjects with one digest. Every subject must still name the image.
func TestAttestationsReadTheSubjectsOfAnSBOM(t *testing.T) {
	cases := []struct {
		name   string
		second string
		reason string
	}{
		{"an image published under two names", "", ""},
		{"a second subject of another image", "0000000000000000000000000000000000000000000000000000000000000000", "describes another image"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := verifytest.NewRegistry(t)
			r.Publish(t)
			index, err := v1.ParseIndexManifest(bytes.NewReader(image(t, "index.json")))
			if err != nil {
				t.Fatal(err)
			}
			index.Manifests[1].Digest = attestationWith(t, r, func(m *v1.Manifest) {
				m.Layers[0] = sbomWithSecondSubject(t, r, c.second)
			})
			_, err = attestations(r.Repo, index, nil)
			if c.reason == "" && err != nil {
				t.Errorf("the check returned %v, want no error", err)
			}
			if c.reason != "" && (err == nil || !strings.Contains(err.Error(), c.reason)) {
				t.Errorf("the check returned %v, want an error that says %q", err, c.reason)
			}
		})
	}
}

// sbomWithSecondSubject pushes a copy of the SBOM's statement with a
// second subject, which names the first subject's digest when digest is
// empty, and returns the layer that points at it.
func sbomWithSecondSubject(t *testing.T, r verifytest.Registry, digest string) v1.Descriptor {
	var statement map[string]any
	if err := json.Unmarshal(image(t, "sbom.intoto.json"), &statement); err != nil {
		t.Fatal(err)
	}
	subjects := statement["subject"].([]any)
	first := subjects[0].(map[string]any)
	if digest == "" {
		digest = first["digest"].(map[string]any)["sha256"].(string)
	}
	statement["subject"] = append(subjects, map[string]any{
		"name":   "pkg:docker/ghcr.io/liken-sh/bluetoothd-other@2026.11.01-001?platform=linux%2Famd64",
		"digest": map[string]any{"sha256": digest},
	})
	data, err := json.Marshal(statement)
	if err != nil {
		t.Fatal(err)
	}
	r.Blob(t, data)
	m, err := v1.ParseManifest(bytes.NewReader(image(t, "attestation.json")))
	if err != nil {
		t.Fatal(err)
	}
	layer := m.Layers[0]
	layer.Digest, layer.Size, _ = v1.SHA256(bytes.NewReader(data))
	return layer
}
