package machine

// The release document defines what one version of liken is, by
// digest.
//
// This document names the release's artifact files with their
// sha256 digests and sizes. It also records which upstream
// components (the kernel, k3s, and the rest) shipped inside them.
// The Cluster's release catalog pins a version to the digest of this
// document's exact bytes. This document, in turn, pins every
// artifact's exact bytes. A machine that checks both digests has
// proven that what it is about to boot is exactly what the catalog
// named. Nothing here is signed, because liken already trusts the
// Kubernetes API, and the catalog lives in it.
//
// Two consumers read this document. The installer reads a copy
// baked beside the artifacts it describes, and verifies it before it
// copies anything to a slot. The operator's release downloader reads
// a copy fetched from the release server, and verifies it against
// the catalog before it fetches any artifact.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/liken-sh/liken/api"
	"sigs.k8s.io/yaml"
)

type Release struct {
	APIVersion string             `json:"apiVersion"`
	Kind       string             `json:"kind"`
	Metadata   api.ObjectMeta     `json:"metadata"`
	Artifacts  []ReleaseArtifact  `json:"artifacts"`
	Components []ReleaseComponent `json:"components,omitempty"`
}

// A ReleaseArtifact names one file of the release. The size is
// informational: the code uses it for progress reporting and
// validity checks. The digest is the identity.
type ReleaseArtifact struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// A ReleaseComponent records one vendored piece of the system, and
// the upstream version of it that shipped: the kernel, k3s, and the
// rest. liken's own version is a calendar date. The date states when
// a release was made, and it says nothing about what is inside the
// release. So this document carries that information instead. The
// versions are informational, each in its own upstream format. The
// artifacts' digests remain the only identity.
type ReleaseComponent struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ParseRelease validates a release document as it reads the
// document, the same way Parse and ParseCluster validate theirs. If
// a document is not exactly what it claims to be, ParseRelease
// rejects it and states the reason. It never accepts a document
// partially.
func ParseRelease(raw []byte) (*Release, error) {
	r := &Release{}
	if err := yaml.UnmarshalStrict(raw, r); err != nil {
		return nil, err
	}
	if r.Kind != "Release" {
		return nil, fmt.Errorf("expected kind Release, got %q", r.Kind)
	}
	if r.Metadata.Name == "" {
		return nil, fmt.Errorf("a release must name its version")
	}
	if len(r.Artifacts) == 0 {
		return nil, fmt.Errorf("release %s lists no artifacts", r.Metadata.Name)
	}
	for _, a := range r.Artifacts {
		if a.Name == "" {
			return nil, fmt.Errorf("release %s has an unnamed artifact", r.Metadata.Name)
		}
		if len(a.SHA256) != 64 {
			return nil, fmt.Errorf("artifact %s: sha256 must be 64 hex characters, got %d", a.Name, len(a.SHA256))
		}
		if _, err := hex.DecodeString(a.SHA256); err != nil {
			return nil, fmt.Errorf("artifact %s: sha256 is not hex: %w", a.Name, err)
		}
	}
	for _, c := range r.Components {
		if c.Name == "" || c.Version == "" {
			return nil, fmt.Errorf("release %s has a component missing its name or version", r.Metadata.Name)
		}
	}
	return r, nil
}

// Verify streams a reader through sha256, and compares the result
// against this artifact's declared digest and size. Because Verify
// streams the data, a 100MB artifact never needs to stay in memory
// all at once. Callers verify by reading again the file they wrote.
// This checks the bytes that actually reached the disk, rather than
// the bytes the caller meant to write.
func (a ReleaseArtifact) Verify(r io.Reader) error {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return fmt.Errorf("reading %s: %w", a.Name, err)
	}
	if n != a.Size {
		return fmt.Errorf("%s is %d bytes, want %d", a.Name, n, a.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != a.SHA256 {
		return fmt.Errorf("%s digest mismatch: got %s, want %s", a.Name, got, a.SHA256)
	}
	return nil
}
