package machine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"slices"
	"strings"
	"testing"
)

func releaseYAML(t *testing.T, body string) *Release {
	t.Helper()
	r, err := ParseRelease([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestParseRelease(t *testing.T) {
	r := releaseYAML(t, `
apiVersion: liken.sh/v1alpha1
kind: Release
metadata:
  name: 2026.07.11-001
artifacts:
  - name: vmlinuz
    sha256: `+strings.Repeat("ab", 32)+`
    size: 17000000
  - name: liken.cpio
    sha256: `+strings.Repeat("cd", 32)+`
    size: 99000000
components:
  - name: kernel
    version: 7.1.2
  - name: k3s
    version: v1.36.2+k3s1
`)
	if r.Metadata.Name != "2026.07.11-001" {
		t.Errorf("version: got %q", r.Metadata.Name)
	}
	if len(r.Artifacts) != 2 || r.Artifacts[0].Name != "vmlinuz" || r.Artifacts[0].Size != 17_000_000 {
		t.Errorf("artifacts: %+v", r.Artifacts)
	}
	if len(r.Components) != 2 || r.Components[0].Name != "kernel" || r.Components[1].Version != "v1.36.2+k3s1" {
		t.Errorf("components: %+v", r.Components)
	}
}

func TestParseReleaseAllowsAbsentComponents(t *testing.T) {
	r := releaseYAML(t, `
apiVersion: liken.sh/v1alpha1
kind: Release
metadata:
  name: 2026.07.11-001
artifacts:
  - name: vmlinuz
    sha256: `+strings.Repeat("ab", 32)+`
    size: 17000000
`)
	if len(r.Components) != 0 {
		t.Errorf("components: %+v", r.Components)
	}
}

func TestParseReleaseVetsAtTheDoor(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	version := "2026.07.11-001"
	cases := map[string]string{
		"wrong kind":            `{kind: Machine, metadata: {name: ` + version + `}, artifacts: [{name: x, sha256: ` + digest + `}]}`,
		"no version":            `{kind: Release, artifacts: [{name: x, sha256: ` + digest + `}]}`,
		"no artifacts":          `{kind: Release, metadata: {name: ` + version + `}}`,
		"unnamed artifact":      `{kind: Release, metadata: {name: ` + version + `}, artifacts: [{sha256: ` + digest + `}]}`,
		"short digest":          `{kind: Release, metadata: {name: ` + version + `}, artifacts: [{name: x, sha256: abcd}]}`,
		"non-hex digest":        `{kind: Release, metadata: {name: ` + version + `}, artifacts: [{name: x, sha256: ` + strings.Repeat("zz", 32) + `}]}`,
		"unknown field":         `{kind: Release, metadata: {name: ` + version + `}, surprise: true, artifacts: [{name: x, sha256: ` + digest + `}]}`,
		"unnamed component":     `{kind: Release, metadata: {name: ` + version + `}, artifacts: [{name: x, sha256: ` + digest + `}], components: [{version: 7.1.2}]}`,
		"unversioned component": `{kind: Release, metadata: {name: ` + version + `}, artifacts: [{name: x, sha256: ` + digest + `}], components: [{name: kernel}]}`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseRelease([]byte(doc)); err == nil {
				t.Error("expected a parse error")
			}
		})
	}
}

func TestArtifactVerify(t *testing.T) {
	content := []byte("the artifact's exact bytes")
	sum := sha256.Sum256(content)
	good := ReleaseArtifact{Name: "x", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content))}

	if err := good.Verify(bytes.NewReader(content)); err != nil {
		t.Errorf("matching bytes should verify: %v", err)
	}
	if err := good.Verify(bytes.NewReader(content[:10])); err == nil {
		t.Error("truncated bytes must not verify")
	}

	tampered := slices.Clone(content)
	tampered[0] ^= 1
	if err := good.Verify(bytes.NewReader(tampered)); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Errorf("tampered bytes must fail on the digest: %v", err)
	}
}

func TestArtifactVerifyReportsReadErrors(t *testing.T) {
	// A directory opened as a file is a real reader whose Read call
	// fails. This is the same failure shape as a download that stops
	// partway through.
	f, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	a := ReleaseArtifact{Name: "x", SHA256: strings.Repeat("ab", 32), Size: 1}
	if err := a.Verify(f); err == nil || !strings.Contains(err.Error(), "reading") {
		t.Errorf("a failing reader must fail the verification: %v", err)
	}
}
