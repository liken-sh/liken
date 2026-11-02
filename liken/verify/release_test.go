package verify

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/verify/verifytest"
)

// serveChannel serves the files as the release at version.
func serveChannel(t *testing.T, version string, files map[string][]byte) Channel {
	server := verifytest.ServeChannel(t, version, files)
	return Channel{Source: server.URL, Client: server.Client()}
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestReleaseSBOMReturnsThePublishedSBOM(t *testing.T) {
	files := verifytest.Published(t)
	ch := serveChannel(t, verifytest.Version, files)
	sbom, err := ch.ReleaseSBOM(t.Context(), verifytest.Version, digestOf(files["release.yaml"]))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sbom, files["sbom.cdx.json"]) {
		t.Error("the SBOM differs from the published file")
	}
}

func TestReleaseSBOMRefuses(t *testing.T) {
	cases := []struct {
		name   string
		change func(files map[string][]byte)
		digest string
		reason string
	}{
		{"a changed byte in the SBOM", func(f map[string][]byte) { f["sbom.cdx.json"] = verifytest.Tampered(f["sbom.cdx.json"]) }, "",
			"sbom.cdx.json digest mismatch"},
		{"the SBOM's bundle swapped for release.yaml's", func(f map[string][]byte) { f["sbom.cdx.json.sigstore.json"] = f["release.yaml.sigstore.json"] }, "",
			"sbom.cdx.json: the signature"},
		{"release.yaml's bundle swapped for the SBOM's", func(f map[string][]byte) { f["release.yaml.sigstore.json"] = f["sbom.cdx.json.sigstore.json"] }, "",
			"release.yaml: the signature"},
		{"no bundle beside release.yaml", func(f map[string][]byte) { delete(f, "release.yaml.sigstore.json") }, "",
			"release.yaml: fetching"},
		{"no bundle beside the SBOM", func(f map[string][]byte) { delete(f, "sbom.cdx.json.sigstore.json") }, "",
			"sbom.cdx.json: fetching"},
		{"a digest pin for other bytes", func(map[string][]byte) {}, "sha256:" + strings.Repeat("0", 64),
			"-digest pins sha256:0000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := verifytest.Published(t)
			c.change(files)
			_, err := serveChannel(t, verifytest.Version, files).ReleaseSBOM(t.Context(), verifytest.Version, c.digest)
			if err == nil || !strings.Contains(err.Error(), c.reason) {
				t.Errorf("the check returned %v, want an error that says %q", err, c.reason)
			}
		})
	}
}

// A channel that answers one version with the signed document of
// another must fail, or a channel could serve an old release under a
// new name.
func TestReleaseDocumentRefusesAnotherVersion(t *testing.T) {
	ch := serveChannel(t, "2026.12.01-001", verifytest.Published(t))
	_, _, err := ch.ReleaseDocument(t.Context(), "2026.12.01-001", "")
	if err == nil || !strings.Contains(err.Error(), "names version 2026.11.01-001, not 2026.12.01-001") {
		t.Errorf("the check returned %v", err)
	}
}

func TestReleaseReportsEachFileAndStopsAtTheFirstFailure(t *testing.T) {
	var out bytes.Buffer
	_, err := serveChannel(t, verifytest.Version, verifytest.Published(t)).Release(t.Context(), verifytest.Version, "", &out)
	want := "ok  release.yaml  signed by https://github.com/liken-sh/liken/.github/workflows/component-liken.yaml@refs/tags/2026.11.01-001\n"
	if out.String() != want {
		t.Errorf("the output is %q, want %q", out.String(), want)
	}
	if err == nil || !strings.HasPrefix(err.Error(), "vmlinuz: fetching") {
		t.Errorf("the check returned %v, want a failure that names vmlinuz", err)
	}
}

func TestCheckSBOMSummarizesThePublishedSBOM(t *testing.T) {
	summary, err := checkSBOM(release(t, "sbom.cdx.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := summary.String(), "744 components: 64 buildroot, 23 manifest, 657 syft"; got != want {
		t.Errorf("the summary is %q, want %q", got, want)
	}
}

func TestCheckSBOMRefusesADocumentOutsideTheSchema(t *testing.T) {
	_, err := checkSBOM([]byte(`{"bomFormat": "CycloneDX", "specVersion": "1.7", "colour": "blue"}`))
	if err == nil || !strings.Contains(err.Error(), "sbom.cdx.json: the SBOM does not follow CycloneDX") {
		t.Errorf("the check returned %v", err)
	}
}
