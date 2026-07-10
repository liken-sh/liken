package machine

// Tests for the deployment layer's sidecar: the digest record that
// rides beside deployment.cpio on a boot slot, written once at
// install time and checked at every carry.

import (
	"bytes"
	"strings"
	"testing"
)

func TestLayerSidecarRoundTrips(t *testing.T) {
	layer := []byte("the deployment layer's bytes")
	installed, err := DigestLayer(bytes.NewReader(layer))
	if err != nil {
		t.Fatal(err)
	}

	digest, err := ParseLayerSidecar(FormatLayerSidecar(installed))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyLayer(digest, bytes.NewReader(layer)); err != nil {
		t.Errorf("the layer does not verify against its own sidecar: %v", err)
	}
}

func TestLayerSidecarUsesTheSha256sumConvention(t *testing.T) {
	sidecar := string(FormatLayerSidecar(strings.Repeat("ab", 32)))
	want := strings.Repeat("ab", 32) + "  deployment.cpio\n"
	if sidecar != want {
		t.Errorf("sidecar %q, want %q (sha256sum -c format)", sidecar, want)
	}
}

func TestParseLayerSidecarRejectsDamage(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"truncated digest", strings.Repeat("ab", 16) + "  deployment.cpio\n"},
		{"not hex", strings.Repeat("zz", 32) + "  deployment.cpio\n"},
		{"wrong file name", strings.Repeat("ab", 32) + "  liken.cpio\n"},
		{"missing name", strings.Repeat("ab", 32) + "\n"},
		{"trailing garbage", strings.Repeat("ab", 32) + "  deployment.cpio\nmore\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParseLayerSidecar([]byte(c.raw)); err == nil {
				t.Errorf("%q parsed; a damaged sidecar must be rejected", c.raw)
			}
		})
	}
}

func TestVerifyLayerRejectsChangedBytes(t *testing.T) {
	digest, err := DigestLayer(strings.NewReader("the layer as installed"))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyLayer(digest, strings.NewReader("the layer, torn")); err == nil {
		t.Error("changed layer bytes must not verify")
	}
}
