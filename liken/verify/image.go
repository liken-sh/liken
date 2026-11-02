package verify

// This file checks an image that a component's publish job pushed to a
// registry, the way `cosign verify` and `docker buildx imagetools
// inspect` check it by hand.
//
// buildx pushes each image as an index. The index names the image's
// manifest for each platform, and beside each one a manifest of the
// platform unknown/unknown that holds the image's attestations: the
// SPDX SBOM and the SLSA provenance of the build, each an in-toto
// statement. The publish job then signs the digest of the index with
// cosign, which pushes the Sigstore bundle as an OCI referrer of that
// digest: a manifest whose subject is the index, with the bundle as
// its one layer. A registry with no referrers API, such as ghcr.io,
// lists the referrers of a digest in an index under the tag
// sha256-<hex>, and go-containerregistry reads either form.
//
// The signature covers the index's digest, and the index names each
// manifest by its digest, and each manifest names each layer by its
// digest. So the check reads every manifest and layer by digest, and
// go-containerregistry checks each digest as it reads, so the SBOM
// that the check reports is one that the signature covers.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

const (
	// bundleArtifactType is the artifact type of the manifest in which
	// cosign stores a Sigstore bundle.
	bundleArtifactType = "application/vnd.dev.sigstore.bundle.v0.3+json"
	// referenceType and referenceDigest are the annotations with which
	// buildx marks an attestation manifest in an index, and names the
	// image manifest that the attestations describe.
	referenceType   = "vnd.docker.reference.type"
	referenceDigest = "vnd.docker.reference.digest"
	// predicateType is the annotation that names an attestation
	// layer's in-toto predicate.
	predicateType = "in-toto.io/predicate-type"
	// spdxPredicate is the predicate of buildx's SBOM, and
	// provenancePrefix starts each version of SLSA's provenance.
	spdxPredicate    = "https://spdx.dev/Document"
	provenancePrefix = "https://slsa.dev/provenance/"
	// blobLimit caps a manifest, a bundle, or an SBOM that the check
	// reads whole. An image's SPDX document is tens of kilobytes.
	blobLimit = 64 << 20
)

// An ImageReport is what a check of an image found.
type ImageReport struct {
	// Reference names the index by its digest.
	Reference string
	// Signer is the workflow that signed the index.
	Signer Signer
	// Attestations holds one entry for each platform of the image.
	Attestations []Attestation
}

// An Attestation is the SBOM and the provenance of one platform's
// image.
type Attestation struct {
	// Platform is the platform of the image, such as linux/amd64.
	Platform string
	// SBOM is the SPDX document, the predicate of the statement.
	SBOM json.RawMessage
	// Packages is the number of packages that the SBOM lists.
	Packages int
	// Provenance is the predicate type of the provenance, which names
	// its SLSA version.
	Provenance string
}

// Image resolves the reference to the digest of its index, checks the
// index's signature against ImageSigner, and reads the SBOM and the
// provenance of each platform. It fails when the reference names no
// index, when no signature of ImageSigner covers the digest, or when a
// platform lacks either attestation.
func Image(ctx context.Context, reference string, options ...remote.Option) (*ImageReport, error) {
	ref, err := name.ParseReference(reference)
	if err != nil {
		return nil, err
	}
	options = append([]remote.Option{remote.WithContext(ctx)}, options...)
	desc, err := remote.Get(ref, options...)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", reference, err)
	}
	if !desc.MediaType.IsIndex() {
		return nil, fmt.Errorf("%s is a %s, and a published image is an index that holds its attestations", reference, desc.MediaType)
	}
	// The digest is the hash of the bytes that the check parses, so the
	// signature covers exactly the index that the check reads.
	sum := sha256.Sum256(desc.Manifest)
	digest := ref.Context().Digest("sha256:" + hex.EncodeToString(sum[:]))
	report := &ImageReport{Reference: digest.String()}

	if report.Signer, err = imageSignature(digest, sum, options); err != nil {
		return nil, err
	}
	index, err := v1.ParseIndexManifest(bytes.NewReader(desc.Manifest))
	if err != nil {
		return nil, fmt.Errorf("the index of %s does not parse: %w", reference, err)
	}
	if report.Attestations, err = attestations(digest.Context(), index, options); err != nil {
		return nil, err
	}
	return report, nil
}

// imageSignature finds the Sigstore bundles that refer to the digest
// and returns the signer of the first one that verifies. A digest can
// carry several signatures, such as one from each publish of a pinned
// component that ran again, and any one of them proves the signer.
func imageSignature(digest name.Digest, sum [sha256.Size]byte, options []remote.Option) (Signer, error) {
	referrers, err := remote.Referrers(digest, options...)
	if err != nil {
		return Signer{}, fmt.Errorf("listing the signatures of %s: %w", digest, err)
	}
	listed, err := referrers.IndexManifest()
	if err != nil {
		return Signer{}, fmt.Errorf("listing the signatures of %s: %w", digest, err)
	}
	var failures []error
	for _, d := range listed.Manifests {
		if d.ArtifactType != bundleArtifactType {
			continue
		}
		bundleJSON, err := signatureBundle(digest.Context().Digest(d.Digest.String()), options)
		if err == nil {
			var signer Signer
			if signer, err = checkBundle(bundleJSON, sum, ImageSigner); err == nil {
				return signer, nil
			}
		}
		failures = append(failures, fmt.Errorf("%s: %w", d.Digest, err))
	}
	if len(failures) == 0 {
		return Signer{}, fmt.Errorf("%s carries no Sigstore signature", digest)
	}
	return Signer{}, fmt.Errorf("no signature of %s verifies: %w", digest, errors.Join(failures...))
}

// signatureBundle reads the bundle that one signature manifest holds
// as its one layer.
func signatureBundle(ref name.Digest, options []remote.Option) ([]byte, error) {
	manifest, err := readManifest(ref, options)
	if err != nil {
		return nil, err
	}
	if len(manifest.Layers) != 1 {
		return nil, fmt.Errorf("the signature manifest holds %d layers, not 1", len(manifest.Layers))
	}
	return readBlob(ref.Context().Digest(manifest.Layers[0].Digest.String()), options)
}

// attestations reads the attestation manifest of each platform of the
// index, and requires an SPDX SBOM and an SLSA provenance in each.
func attestations(repo name.Repository, index *v1.IndexManifest, options []remote.Option) ([]Attestation, error) {
	platforms := map[v1.Hash]string{}
	attested := map[v1.Hash]v1.Hash{}
	for _, d := range index.Manifests {
		if d.Annotations[referenceType] == "attestation-manifest" {
			subject, err := v1.NewHash(d.Annotations[referenceDigest])
			if err != nil {
				return nil, fmt.Errorf("the attestation manifest %s names no image: %w", d.Digest, err)
			}
			attested[subject] = d.Digest
		} else if d.Platform != nil {
			platforms[d.Digest] = d.Platform.String()
		}
	}
	var found []Attestation
	for _, d := range index.Manifests {
		platform, ok := platforms[d.Digest]
		if !ok {
			continue
		}
		manifest, ok := attested[d.Digest]
		if !ok {
			return nil, fmt.Errorf("the %s image carries no attestations", platform)
		}
		a, err := attestation(repo, platform, d.Digest, manifest, options)
		if err != nil {
			return nil, err
		}
		found = append(found, a)
	}
	if len(found) == 0 {
		return nil, errors.New("the index names no image")
	}
	return found, nil
}

// attestation reads one attestation manifest: the SBOM's statement,
// which must describe the platform's image, and the predicate type of
// the provenance.
func attestation(repo name.Repository, platform string, image, manifestDigest v1.Hash, options []remote.Option) (Attestation, error) {
	a := Attestation{Platform: platform}
	manifest, err := readManifest(repo.Digest(manifestDigest.String()), options)
	if err != nil {
		return a, fmt.Errorf("the attestations of the %s image: %w", platform, err)
	}
	var sbom *v1.Descriptor
	for _, layer := range manifest.Layers {
		switch predicate := layer.Annotations[predicateType]; {
		case predicate == spdxPredicate:
			sbom = &layer
		case strings.HasPrefix(predicate, provenancePrefix):
			a.Provenance = predicate
		}
	}
	if sbom == nil {
		return a, fmt.Errorf("the %s image carries no SPDX SBOM", platform)
	}
	if a.Provenance == "" {
		return a, fmt.Errorf("the %s image carries no SLSA provenance", platform)
	}
	data, err := readBlob(repo.Digest(sbom.Digest.String()), options)
	if err != nil {
		return a, fmt.Errorf("the SBOM of the %s image: %w", platform, err)
	}
	var statement struct {
		PredicateType string `json:"predicateType"`
		Subject       []struct {
			Digest map[string]string `json:"digest"`
		} `json:"subject"`
		Predicate json.RawMessage `json:"predicate"`
	}
	if err := json.Unmarshal(data, &statement); err != nil {
		return a, fmt.Errorf("the SBOM of the %s image does not parse: %w", platform, err)
	}
	if statement.PredicateType != spdxPredicate {
		return a, fmt.Errorf("the SBOM of the %s image states the predicate %s", platform, statement.PredicateType)
	}
	// buildx lists the image once for each name the build published it
	// under, so an image with two names has two subjects. Each one must
	// name this image's digest.
	if len(statement.Subject) == 0 {
		return a, fmt.Errorf("the SBOM of the %s image describes another image", platform)
	}
	for _, subject := range statement.Subject {
		if subject.Digest[image.Algorithm] != image.Hex {
			return a, fmt.Errorf("the SBOM of the %s image describes another image", platform)
		}
	}
	packages, err := SPDXPackages(statement.Predicate)
	if err != nil {
		return a, err
	}
	a.SBOM, a.Packages = statement.Predicate, len(packages)
	return a, nil
}

// readManifest reads an image manifest by its digest.
func readManifest(ref name.Digest, options []remote.Option) (*v1.Manifest, error) {
	desc, err := remote.Get(ref, options...)
	if err != nil {
		return nil, err
	}
	return v1.ParseManifest(bytes.NewReader(desc.Manifest))
}

// readBlob reads a blob by its digest. go-containerregistry checks the
// digest when the read reaches the end.
func readBlob(ref name.Digest, options []remote.Option) ([]byte, error) {
	layer, err := remote.Layer(ref, options...)
	if err != nil {
		return nil, err
	}
	rc, err := layer.Compressed()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, blobLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > blobLimit {
		return nil, fmt.Errorf("%s is larger than %d bytes", ref, blobLimit)
	}
	return data, nil
}
