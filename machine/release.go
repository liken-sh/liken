package machine

// The release document defines what one version of liken is, by
// digest.
//
// A liken release is two files, vmlinuz and liken.cpio, and this
// document names them with their sha256 digests and sizes. It is the
// middle link of the trust chain: the Cluster's release catalog pins
// a version to the digest of this document's exact bytes, and this
// document pins every artifact's exact bytes. A machine that verifies
// the chain end to end has proven that what it is about to boot is
// exactly what the catalog named. Nothing here is signed; liken
// already trusts the Kubernetes API, and the catalog lives in it.
//
// Two consumers read it: the installer (a copy baked beside the
// artifacts it describes, verified before anything is copied to a
// slot) and the operator's release downloader (fetched from the
// release server, verified against the catalog before any artifact
// is fetched at all).

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"sigs.k8s.io/yaml"
)

type Release struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   ObjectMeta        `json:"metadata"`
	Artifacts  []ReleaseArtifact `json:"artifacts"`
}

// A ReleaseArtifact names one file of the release. The size is
// informational (progress reporting, sanity checks); the digest is
// the identity.
type ReleaseArtifact struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// ParseRelease validates a release document as it is read, the way
// Parse and ParseCluster do for theirs: a document that isn't exactly
// what it claims to be is rejected with the reason, never partially
// accepted.
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
	return r, nil
}

// Artifact addresses one artifact by name; nil when the release
// doesn't carry it.
func (r *Release) Artifact(name string) *ReleaseArtifact {
	for i := range r.Artifacts {
		if r.Artifacts[i].Name == name {
			return &r.Artifacts[i]
		}
	}
	return nil
}

// Verify streams a reader through sha256 and compares the result
// against this artifact's declared digest and size. Streaming means a
// 100MB artifact never needs to be held in memory. Callers verify by
// re-reading the file they wrote, which checks the bytes that
// actually landed rather than the bytes they meant to write.
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
