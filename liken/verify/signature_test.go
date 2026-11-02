package verify

import (
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/verify/verifytest"
)

// release reads one published file of the fixture release.
func release(t *testing.T, name string) []byte { return verifytest.ReleaseFile(t, name) }

func TestCheckBundleAcceptsThePublishedSignature(t *testing.T) {
	cases := []string{"release.yaml", "sbom.cdx.json"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			signer, err := checkBundle(release(t, name+".sigstore.json"), sha256.Sum256(release(t, name)), ReleaseSigner)
			if err != nil {
				t.Fatal(err)
			}
			want := "https://github.com/liken-sh/liken/.github/workflows/component-liken.yaml@refs/tags/2026.11.01-001"
			if signer.Workflow != want {
				t.Errorf("the signer is %s, want %s", signer.Workflow, want)
			}
		})
	}
}

func TestCheckBundleRefuses(t *testing.T) {
	cases := []struct {
		name     string
		bundle   string
		file     []byte
		identity Identity
		reason   string
	}{
		{"a changed byte", "sbom.cdx.json.sigstore.json", verifytest.Tampered(release(t, "sbom.cdx.json")), ReleaseSigner, "does not verify"},
		{"the bundle of another file", "release.yaml.sigstore.json", release(t, "sbom.cdx.json"), ReleaseSigner, "does not verify"},
		{"another workflow", "release.yaml.sigstore.json", release(t, "release.yaml"),
			Identity{Issuer: githubIssuer, SAN: `^https://github\.com/liken-sh/liken/\.github/workflows/component-media-operator\.yaml@.*$`}, "certificate identity"},
		{"another issuer", "release.yaml.sigstore.json", release(t, "release.yaml"),
			Identity{Issuer: "https://accounts.google.com", SAN: ReleaseSigner.SAN}, "certificate identity"},
		{"a bundle that is not JSON", "release.yaml", release(t, "release.yaml"), ReleaseSigner, "does not parse"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := checkBundle(release(t, c.bundle), sha256.Sum256(c.file), c.identity)
			if err == nil || !strings.Contains(err.Error(), c.reason) {
				t.Errorf("the check returned %v, want an error that says %q", err, c.reason)
			}
		})
	}
}
