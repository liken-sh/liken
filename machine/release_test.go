package machine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
  name: 0.2.0
artifacts:
  - name: vmlinuz
    sha256: `+strings.Repeat("ab", 32)+`
    size: 17000000
  - name: liken.cpio
    sha256: `+strings.Repeat("cd", 32)+`
    size: 99000000
`)
	if r.Metadata.Name != "0.2.0" {
		t.Errorf("version: got %q", r.Metadata.Name)
	}
	if a := r.Artifact("vmlinuz"); a == nil || a.Size != 17_000_000 {
		t.Errorf("vmlinuz artifact: %+v", a)
	}
	if r.Artifact("nonexistent") != nil {
		t.Error("unknown artifacts should be nil")
	}
}

func TestParseReleaseVetsAtTheDoor(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	cases := map[string]string{
		"wrong kind":       `{kind: Machine, metadata: {name: 0.2.0}, artifacts: [{name: x, sha256: ` + digest + `}]}`,
		"no version":       `{kind: Release, artifacts: [{name: x, sha256: ` + digest + `}]}`,
		"no artifacts":     `{kind: Release, metadata: {name: 0.2.0}}`,
		"unnamed artifact": `{kind: Release, metadata: {name: 0.2.0}, artifacts: [{sha256: ` + digest + `}]}`,
		"short digest":     `{kind: Release, metadata: {name: 0.2.0}, artifacts: [{name: x, sha256: abcd}]}`,
		"non-hex digest":   `{kind: Release, metadata: {name: 0.2.0}, artifacts: [{name: x, sha256: ` + strings.Repeat("zz", 32) + `}]}`,
		"unknown field":    `{kind: Release, metadata: {name: 0.2.0}, surprise: true, artifacts: [{name: x, sha256: ` + digest + `}]}`,
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

	tampered := append([]byte(nil), content...)
	tampered[0] ^= 1
	if err := good.Verify(bytes.NewReader(tampered)); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Errorf("tampered bytes must fail on the digest: %v", err)
	}
}
