package verify

// This file checks an OS release on a channel the way a person checks
// it with cosign: the Sigstore bundle of release.yaml, then each
// artifact that release.yaml names, against its digest and against
// its own bundle.
//
// The digest in release.yaml already binds each artifact, and a
// machine trusts an artifact through that digest alone. The bundle of
// each artifact is checked too, because it costs one small download,
// and because a person who downloads one file, such as the SBOM or the
// toolkit, checks that file's own bundle without release.yaml.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/releases"
	"github.com/liken-sh/liken/liken/releases/sbom/schema"
)

// DefaultSource is the release channel that `liken`'s CI publishes to.
const DefaultSource = "https://releases.liken.sh"

// SBOMName is the artifact of a release that holds its CycloneDX SBOM.
const SBOMName = "sbom.cdx.json"

// bundleSuffix names the Sigstore bundle of a file: the bundle of
// release.yaml is release.yaml.sigstore.json, beside it.
const bundleSuffix = ".sigstore.json"

// documentLimit caps a document that the check reads whole: a
// release.yaml is about two kilobytes and a bundle about ten.
const documentLimit = 1 << 20

// A Channel is a release channel to read from: its base URL, and the
// client that reads it.
type Channel struct {
	Source string
	Client *http.Client
}

// open starts the download of one file of a release. The caller
// closes the body.
func (c Channel) open(ctx context.Context, version, name string) (io.ReadCloser, error) {
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	url := strings.TrimSuffix(c.Source, "/") + "/" + version + "/" + name
	resp, err := releases.Get(ctx, client, url)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	return resp.Body, nil
}

// document downloads one small file of a release whole.
func (c Channel) document(ctx context.Context, version, name string) ([]byte, error) {
	body, err := c.open(ctx, version, name)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, documentLimit+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}
	if len(data) > documentLimit {
		return nil, fmt.Errorf("%s is larger than %d bytes", name, documentLimit)
	}
	return data, nil
}

// ReleaseDocument downloads release.yaml and checks it. digest, when
// it is not empty, pins the document's bytes to a catalog entry's
// "sha256:<hex>", and the check of the pin runs before the signature.
// The document must carry the signature of ReleaseSigner and name the
// version that the caller asked for, so a channel cannot answer one
// version with the signed document of another.
func (c Channel) ReleaseDocument(ctx context.Context, version, digest string) (*machine.Release, Signer, error) {
	raw, err := c.document(ctx, version, "release.yaml")
	if err != nil {
		return nil, Signer{}, fmt.Errorf("release.yaml: %w", err)
	}
	sum := sha256.Sum256(raw)
	if got := "sha256:" + hex.EncodeToString(sum[:]); digest != "" && got != digest {
		return nil, Signer{}, fmt.Errorf("release.yaml: the digest is %s, and -digest pins %s", got, digest)
	}
	bundleJSON, err := c.document(ctx, version, "release.yaml"+bundleSuffix)
	if err != nil {
		return nil, Signer{}, fmt.Errorf("release.yaml: %w", err)
	}
	signer, err := checkBundle(bundleJSON, sum, ReleaseSigner)
	if err != nil {
		return nil, Signer{}, fmt.Errorf("release.yaml: %w", err)
	}
	release, err := machine.ParseRelease(raw)
	if err != nil {
		return nil, Signer{}, fmt.Errorf("release.yaml does not parse: %w", err)
	}
	if release.Metadata.Name != version {
		return nil, Signer{}, fmt.Errorf("release.yaml names version %s, not %s", release.Metadata.Name, version)
	}
	return release, signer, nil
}

// Artifact downloads one artifact and checks its size and digest
// against release.yaml, then its bundle against the same digest. The
// bytes stream through the hash once and are not stored, so a system
// image of a gigabyte needs no memory or disk. keep, when it is not
// nil, receives a copy of the bytes, for a caller that reads a small
// artifact such as the SBOM.
func (c Channel) Artifact(ctx context.Context, version string, artifact machine.ReleaseArtifact, keep io.Writer) error {
	bundleJSON, err := c.document(ctx, version, artifact.Name+bundleSuffix)
	if err != nil {
		return fmt.Errorf("%s: %w", artifact.Name, err)
	}
	body, err := c.open(ctx, version, artifact.Name)
	if err != nil {
		return fmt.Errorf("%s: %w", artifact.Name, err)
	}
	defer body.Close()
	var r io.Reader = io.LimitReader(body, artifact.Size+1)
	if keep != nil {
		r = io.TeeReader(r, keep)
	}
	// ReleaseArtifact.Verify is the check that a machine runs on each
	// artifact that it downloads: the size, then the sha256.
	if err := artifact.Verify(r); err != nil {
		return err
	}
	digest, err := hex.DecodeString(artifact.SHA256)
	if err != nil || len(digest) != sha256.Size {
		return fmt.Errorf("%s: release.yaml names the digest %q, which is not a sha256", artifact.Name, artifact.SHA256)
	}
	if _, err := checkBundle(bundleJSON, [sha256.Size]byte(digest), ReleaseSigner); err != nil {
		return fmt.Errorf("%s: %w", artifact.Name, err)
	}
	return nil
}

// Release checks release.yaml and every artifact that it names, and
// writes one line to out for each file that passes. It stops at the
// first file that fails, and its error names that file. The SBOM must
// also follow the CycloneDX schema, and Release returns its summary.
func (c Channel) Release(ctx context.Context, version, digest string, out io.Writer) (Summary, error) {
	release, signer, err := c.ReleaseDocument(ctx, version, digest)
	if err != nil {
		return Summary{}, err
	}
	fmt.Fprintf(out, "ok  release.yaml  signed by %s\n", signer.Workflow)
	var sbom *bytes.Buffer
	for _, artifact := range release.Artifacts {
		var keep io.Writer
		if artifact.Name == SBOMName {
			sbom = &bytes.Buffer{}
			keep = sbom
		}
		if err := c.Artifact(ctx, version, artifact, keep); err != nil {
			return Summary{}, err
		}
		fmt.Fprintf(out, "ok  %s\n", artifact.Name)
	}
	if sbom == nil {
		return Summary{}, fmt.Errorf("release.yaml names no %s", SBOMName)
	}
	return checkSBOM(sbom.Bytes())
}

// checkSBOM checks a release's SBOM against the CycloneDX schema that
// releases/sbom writes it with, and counts its components.
func checkSBOM(data []byte) (Summary, error) {
	if err := schema.Validate(data); err != nil {
		return Summary{}, fmt.Errorf("%s: %w", SBOMName, err)
	}
	return summarize(data)
}

// ReleaseSBOM checks release.yaml and the SBOM that it names, and
// returns the SBOM's bytes. It checks the same signatures and digest
// that Release checks for those two files, and downloads no other
// artifact.
func (c Channel) ReleaseSBOM(ctx context.Context, version, digest string) ([]byte, error) {
	release, _, err := c.ReleaseDocument(ctx, version, digest)
	if err != nil {
		return nil, err
	}
	for _, artifact := range release.Artifacts {
		if artifact.Name != SBOMName {
			continue
		}
		var sbom bytes.Buffer
		if err := c.Artifact(ctx, version, artifact, &sbom); err != nil {
			return nil, err
		}
		if _, err := checkSBOM(sbom.Bytes()); err != nil {
			return nil, err
		}
		return sbom.Bytes(), nil
	}
	return nil, fmt.Errorf("release.yaml names no %s", SBOMName)
}
