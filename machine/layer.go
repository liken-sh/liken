package machine

// This file covers the deployment layer and its sidecar file. The
// deployment layer is the part of the OS that belongs to one
// cluster. The sidecar file lets a machine confirm the layer's
// integrity.
//
// A boot slot carries the release's public artifacts. The release
// document names these artifacts, and the catalog's digest chain
// carries their integrity. A boot slot also carries one file that
// belongs to no release: the deployment layer (deployment.cpio). The
// deployment layer is private to one cluster, and it never travels
// over the network. So the release document cannot name it. Instead,
// a machine carries its own deployment layer forward from slot to
// slot at every upgrade.
//
// The sidecar file makes that carry safe. It is a one-line file that
// names the layer's sha256 digest. The machine writes the sidecar
// file durably at install time, from bytes it has just verified. The
// machine checks the sidecar file again before it trusts any copy of
// the layer.
//
// The sidecar's format matches sha256sum's own format:
// "<digest>  <name>". So a person at a rescue prompt can check a
// slot with a tool that exists everywhere: `sha256sum -c
// deployment.cpio.sha256`.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

// LayerName is the deployment layer's file name, on install media
// and on every boot slot. Boot entries name LayerName as their
// second initrd= parameter, after the generic archive. The kernel
// unpacks initrd parameters in order, and each later entry overrides
// the entries before it.
const LayerName = "deployment.cpio"

// LayerSidecarName is the sidecar's file name. The sidecar file sits
// beside the layer, wherever the layer is.
const LayerSidecarName = LayerName + ".sha256"

// DigestLayer streams a layer through sha256 and returns the hex
// digest. A caller must digest the bytes that actually landed on
// disk, by rereading them. A caller must never digest the bytes it
// meant to write instead.
func DigestLayer(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("reading the layer: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// FormatLayerSidecar writes the sidecar's single line for a layer
// digest.
func FormatLayerSidecar(digest string) []byte {
	return []byte(digest + "  " + LayerName + "\n")
}

// ParseLayerSidecar reads a sidecar file back, with strict checks.
// The file must hold exactly one well-formed line that names the
// layer. Anything else counts as damage: truncation, a torn write,
// or the wrong file. When ParseLayerSidecar finds damage, the caller
// must treat the layer as unverifiable. The caller must not guess at
// the layer's digest.
func ParseLayerSidecar(raw []byte) (string, error) {
	want := FormatLayerSidecar("")
	if len(raw) != 64+len(want) {
		return "", fmt.Errorf("the layer sidecar is %d bytes, want %d", len(raw), 64+len(want))
	}
	digest := string(raw[:64])
	if _, err := hex.DecodeString(digest); err != nil {
		return "", fmt.Errorf("the layer sidecar's digest is not hex: %w", err)
	}
	if string(raw[64:]) != string(want) {
		return "", fmt.Errorf("the layer sidecar names %q, want %q", string(raw[64:]), string(want))
	}
	return digest, nil
}

// VerifyLayer streams a layer through sha256 and compares the result
// against the digest that a sidecar file named.
func VerifyLayer(digest string, r io.Reader) error {
	got, err := DigestLayer(r)
	if err != nil {
		return err
	}
	if got != digest {
		return fmt.Errorf("%s digest mismatch: got %s, want %s", LayerName, got, digest)
	}
	return nil
}
