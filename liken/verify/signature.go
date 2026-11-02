// Package verify checks that a file or an image came from `liken`'s CI
// unchanged, before a person or a program uses it.
//
// CI signs what it publishes keyless, with Sigstore. Fulcio issues a
// short-lived certificate to the GitHub Actions workflow that runs the
// publish, Rekor records each signature in its public log, and the
// Sigstore bundle of each signature holds the certificate, the log's
// receipt, and a signed timestamp. A check of a bundle answers three
// questions: was the certificate issued by Fulcio to a workflow of this
// repository at a release ref, does the signature cover these exact
// bytes, and does Rekor hold the signature. cosign answers the same
// questions with `verify-blob` and `verify`, and this package answers
// them in-process with sigstore-go, so a person needs only the `liken`
// binary.
//
// The check reads Sigstore's keys from trusted_root.json, which is
// pinned in this directory and compiled into the binary. A check never
// fetches the root, so the network can serve a wrong file but cannot
// change which keys judge it. latest.sh beside the file reports and
// bumps the pin.
package verify

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// trustedRootJSON is Sigstore's trusted root: the keys of Fulcio, of
// Rekor's logs, of the certificate transparency logs, and of the
// timestamp authority, each with the time span in which it signed.
//
//go:embed trusted_root.json
var trustedRootJSON []byte

// githubIssuer is the OIDC issuer of a GitHub Actions job. Fulcio
// writes it into each certificate that it issues to a workflow.
const githubIssuer = "https://token.actions.githubusercontent.com"

// An Identity is the signer that a certificate must name: the OIDC
// issuer that vouched for the signer, and a regular expression that
// the certificate's subject alternative name must match. For a GitHub
// workflow the name is the workflow's URL at the ref it ran at.
type Identity struct {
	Issuer string
	SAN    string
}

// ReleaseSigner is the publish job of the OS. The OS publishes from
// release tags alone, so the identity names no branch.
var ReleaseSigner = Identity{
	Issuer: githubIssuer,
	SAN:    `^https://github\.com/liken-sh/liken/\.github/workflows/component-liken\.yaml@refs/tags/[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3}$`,
}

// ImageSigner is the publish job of any component that publishes
// images. A component publishes a development build from main and a
// release from a release tag, and a branch or a pull request never
// publishes, so the identity refuses a signature from any other ref.
var ImageSigner = Identity{
	Issuer: githubIssuer,
	SAN:    `^https://github\.com/liken-sh/liken/\.github/workflows/component-[a-z0-9-]+\.yaml@refs/(heads/main|tags/[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3})$`,
}

// verifier checks bundles against the embedded trusted root. It needs:
//
//   - a signed certificate timestamp from a certificate transparency
//     log, which proves that Fulcio published the certificate;
//   - an entry in Rekor's log, checked offline through the log's
//     signed promise or its inclusion proof and checkpoint;
//   - one observed time, from Rekor's entry or from the timestamp
//     authority, inside the certificate's ten minutes of validity.
//
// The time comes from the bundle and never from this machine's clock.
// A Fulcio certificate expires minutes after the signature, so a check
// against the current time would refuse every release, and a bundle
// verifies the same way on every day after it was made.
var verifier = sync.OnceValues(func() (*verify.Verifier, error) {
	trusted, err := root.NewTrustedRootFromJSON(trustedRootJSON)
	if err != nil {
		return nil, fmt.Errorf("the embedded Sigstore trusted root does not parse: %w", err)
	}
	return verify.NewVerifier(trusted,
		verify.WithSignedCertificateTimestamps(1),
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1))
})

// A Signer is what a verified certificate says about the workflow that
// signed: its URL at the ref it ran at, such as
// https://github.com/liken-sh/liken/.github/workflows/component-liken.yaml@refs/tags/2026.11.01-001.
type Signer struct {
	Workflow string
}

// checkBundle verifies a Sigstore bundle, as JSON, over the sha256
// digest of the signed bytes, and requires the identity. A bundle of a
// file signs the digest directly. A bundle of an image signs an
// in-toto statement whose subject is the digest of the image's index,
// and the check requires that subject. Either way the caller hashes
// the bytes once, so a large file streams through the hash and is
// never held in memory.
func checkBundle(bundleJSON []byte, digest [sha256.Size]byte, id Identity) (Signer, error) {
	v, err := verifier()
	if err != nil {
		return Signer{}, err
	}
	var b bundle.Bundle
	if err := b.UnmarshalJSON(bundleJSON); err != nil {
		return Signer{}, fmt.Errorf("the bundle does not parse: %w", err)
	}
	certID, err := verify.NewShortCertificateIdentity(id.Issuer, "", "", id.SAN)
	if err != nil {
		return Signer{}, err
	}
	result, err := v.Verify(&b, verify.NewPolicy(
		verify.WithArtifactDigest("sha256", digest[:]),
		verify.WithCertificateIdentity(certID)))
	if err != nil {
		return Signer{}, fmt.Errorf("the signature over sha256:%s does not verify: %w", hex.EncodeToString(digest[:]), err)
	}
	return Signer{Workflow: result.Signature.Certificate.SubjectAlternativeName}, nil
}
