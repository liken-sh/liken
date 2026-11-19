package machine

import (
	"bytes"
	"strings"
	"testing"
)

func TestLayerSidecarRoundTrips(t *testing.T) {
	layer := []byte("the deployment layer's bytes")
	installed, err := DigestLayer(bytes.NewReader(layer))
	if err != nil {
		t.Fatalf("digesting the layer: %v", err)
	}

	digest, err := ParseLayerSidecar(FormatLayerSidecar(installed))
	if err != nil {
		t.Fatalf("parsing the sidecar: %v", err)
	}
	if err := VerifyLayer(digest, bytes.NewReader(layer)); err != nil {
		t.Errorf("verifying the layer against its own sidecar: %v, want no error", err)
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
				t.Errorf("parsing the damaged sidecar %q returned no error, want one", c.raw)
			}
		})
	}
}

func TestVerifyLayerRejectsChangedBytes(t *testing.T) {
	digest, err := DigestLayer(strings.NewReader("the layer as installed"))
	if err != nil {
		t.Fatalf("digesting the layer: %v", err)
	}
	if err := VerifyLayer(digest, strings.NewReader("the layer, torn")); err == nil {
		t.Error("verifying changed layer bytes returned no error, want one")
	}
}
