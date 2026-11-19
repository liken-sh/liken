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
		t.Fatalf("parsing the release: %v", err)
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
		t.Errorf("the version is %q, want 2026.07.11-001", r.Metadata.Name)
	}
	if len(r.Artifacts) != 2 || r.Artifacts[0].Name != "vmlinuz" || r.Artifacts[0].Size != 17_000_000 {
		t.Errorf("the artifacts are %+v, want vmlinuz of 17000000 bytes and liken.cpio", r.Artifacts)
	}
	if len(r.Components) != 2 || r.Components[0].Name != "kernel" || r.Components[1].Version != "v1.36.2+k3s1" {
		t.Errorf("the components are %+v, want kernel and k3s v1.36.2+k3s1", r.Components)
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
		t.Errorf("the components are %+v, want none", r.Components)
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
				t.Error("the document parsed, want an error")
			}
		})
	}
}

func TestArtifactVerify(t *testing.T) {
	content := []byte("the artifact's exact bytes")
	sum := sha256.Sum256(content)
	good := ReleaseArtifact{Name: "x", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content))}

	if err := good.Verify(bytes.NewReader(content)); err != nil {
		t.Errorf("verifying the matching bytes: %v, want no error", err)
	}
	if err := good.Verify(bytes.NewReader(content[:10])); err == nil {
		t.Error("the truncated bytes verified, want an error")
	}

	tampered := slices.Clone(content)
	tampered[0] ^= 1
	if err := good.Verify(bytes.NewReader(tampered)); err == nil {
		t.Errorf("verifying the tampered bytes: %v, want an error", err)
	}
}

func TestArtifactVerifyReportsReadErrors(t *testing.T) {
	// Read on an open directory fails, as a read of an interrupted
	// download does.
	f, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatalf("opening a directory: %v", err)
	}
	defer f.Close()
	a := ReleaseArtifact{Name: "x", SHA256: strings.Repeat("ab", 32), Size: 1}
	if err := a.Verify(f); err == nil {
		t.Errorf("verifying a reader that fails: %v, want an error", err)
	}
}
