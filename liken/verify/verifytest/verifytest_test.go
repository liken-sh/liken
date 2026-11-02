package verifytest

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// release.yaml names the SBOM by its digest, so the decompressed
// fixture must be the published bytes.
func TestTheSBOMIsThePublishedFile(t *testing.T) {
	sum := sha256.Sum256(ReleaseFile(t, "sbom.cdx.json"))
	if !strings.Contains(string(ReleaseFile(t, "release.yaml")), "sha256: "+hex.EncodeToString(sum[:])) {
		t.Error("release.yaml does not name the fixture SBOM's digest")
	}
}

func TestTheRegistryServesTheIndexAtItsPublishedDigest(t *testing.T) {
	r := NewRegistry(t)
	r.Publish(t)
	r.Sign(t, ImageFile(t, "signature.sigstore.json"))
	desc, err := remote.Get(r.Repo.Tag(Version))
	if err != nil {
		t.Fatal(err)
	}
	if desc.Digest.String() != IndexDigest {
		t.Errorf("the tag names %s, want %s", desc.Digest, IndexDigest)
	}
	referrers, err := remote.Referrers(r.Repo.Digest(IndexDigest))
	if err != nil {
		t.Fatal(err)
	}
	listed, err := referrers.IndexManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Manifests) != 1 {
		t.Errorf("the index has %d referrers, want 1", len(listed.Manifests))
	}
}

func TestSignListsAnotherBundle(t *testing.T) {
	r := NewRegistry(t)
	r.Publish(t)
	r.Sign(t, ReleaseFile(t, "release.yaml.sigstore.json"))
	referrers, err := remote.Referrers(r.Repo.Digest(IndexDigest))
	if err != nil {
		t.Fatal(err)
	}
	listed, err := referrers.IndexManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Manifests) != 1 || listed.Manifests[0].Digest.String() == "sha256:7999452ff28d75a8e781ce647f72a19a505934984b55765608f8f3fa90e01557" {
		t.Errorf("the referrers are %+v, want one manifest that is not the published signature", listed.Manifests)
	}
}
