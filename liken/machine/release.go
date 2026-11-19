package machine

// A release document, release.yaml, lists the artifact files of one
// liken version with the sha256 digest and size of each, and the
// versions of the upstream components in them.
//
// The Cluster's release catalog pins each version to the sha256 of
// its release document, and the document pins the sha256 of each
// artifact. So a machine that checks both boots only the bytes that
// the catalog named. machine-operator does not check the Sigstore
// signature that the release channel publishes beside the document,
// because it reads the catalog from the Kubernetes API, which it
// already trusts. liken verify release checks that signature.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/liken-sh/liken/liken/api"
	"sigs.k8s.io/yaml"
)

// Release is the parsed release document.
type Release struct {
	APIVersion string             `json:"apiVersion"`
	Kind       string             `json:"kind"`
	Metadata   api.ObjectMeta     `json:"metadata"`
	Artifacts  []ReleaseArtifact  `json:"artifacts"`
	Components []ReleaseComponent `json:"components,omitempty"`
}

// ReleaseArtifact is one file of the release. A download uses Size to
// report progress and to stop reading a body that is longer, and
// Verify compares it before the digest.
type ReleaseArtifact struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// ReleaseComponent is an upstream component, such as the kernel or
// k3s, and the version of it that the release ships. A liken version
// is a date and tells a person nothing about the components, so the
// document lists them. Each version is in the upstream's own format
// and is only for people to read.
type ReleaseComponent struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ParseRelease parses a release document strictly and validates it.
func ParseRelease(raw []byte) (*Release, error) {
	r := &Release{}
	if err := yaml.UnmarshalStrict(raw, r); err != nil {
		return nil, err
	}
	if r.Kind != "Release" {
		return nil, fmt.Errorf("the kind is %q, and a release must have kind Release", r.Kind)
	}
	if r.Metadata.Name == "" {
		return nil, fmt.Errorf("the release has no version in metadata.name")
	}
	if len(r.Artifacts) == 0 {
		return nil, fmt.Errorf("release %s has no artifacts", r.Metadata.Name)
	}
	for _, a := range r.Artifacts {
		if a.Name == "" {
			return nil, fmt.Errorf("release %s has an artifact with no name", r.Metadata.Name)
		}
		if len(a.SHA256) != 64 {
			return nil, fmt.Errorf("artifact %s: the sha256 has %d characters, and it must have 64 hex characters", a.Name, len(a.SHA256))
		}
		if _, err := hex.DecodeString(a.SHA256); err != nil {
			return nil, fmt.Errorf("artifact %s: the sha256 is not hex: %w", a.Name, err)
		}
	}
	for _, c := range r.Components {
		if c.Name == "" || c.Version == "" {
			return nil, fmt.Errorf("release %s has a component with no name or no version", r.Metadata.Name)
		}
	}
	return r, nil
}

// Verify compares the size and sha256 of the reader's bytes with the
// artifact's. It streams, so a large artifact is never held in
// memory. Callers verify the file they wrote by reading it back, so
// the check covers the bytes on the disk.
func (a ReleaseArtifact) Verify(r io.Reader) error {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return fmt.Errorf("reading artifact %s: %w", a.Name, err)
	}
	if n != a.Size {
		return fmt.Errorf("artifact %s has %d bytes, want %d", a.Name, n, a.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != a.SHA256 {
		return fmt.Errorf("artifact %s has sha256 %s, want %s", a.Name, got, a.SHA256)
	}
	return nil
}
