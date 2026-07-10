package machine

// The deployment layer and its sidecar: the half of the OS that is
// yours, and the record that lets a machine vouch for it.
//
// A boot slot carries two initramfs archives. The generic liken.cpio
// is public and appears in the release document, so its integrity
// rides the catalog's digest chain. The deployment layer
// (deployment.cpio) is private to one cluster and never travels over
// the network, so the document cannot name it — a machine carries its
// own layer forward from slot to slot at every upgrade. The sidecar
// is what makes that carry safe: a one-line file naming the layer's
// sha256, written durably at install time from bytes that had just
// been verified, and checked again before any copy of the layer is
// trusted.
//
// The sidecar's format is sha256sum's own ("<digest>  <name>"), so a
// person at a rescue prompt can check a slot with a tool that exists
// everywhere: `sha256sum -c deployment.cpio.sha256`.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

// LayerName is the deployment layer's file name, on install media
// and on every boot slot. Boot entries name it as their second
// initrd= parameter, after the generic archive, because the kernel
// unpacks them in order and later entries override earlier ones.
const LayerName = "deployment.cpio"

// LayerSidecarName is the sidecar's file name, beside the layer
// wherever the layer is.
const LayerSidecarName = LayerName + ".sha256"

// DigestLayer streams a layer through sha256 and returns the hex
// digest. Callers digest the bytes that actually landed on disk by
// re-reading them, never the bytes they meant to write.
func DigestLayer(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("reading the layer: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// FormatLayerSidecar renders the sidecar's single line for a layer
// digest.
func FormatLayerSidecar(digest string) []byte {
	return []byte(digest + "  " + LayerName + "\n")
}

// ParseLayerSidecar reads a sidecar back, strictly: the file must be
// exactly one well-formed line naming the layer. Anything else —
// truncation, a torn write, the wrong file — is damage, and the
// caller must treat the layer as unverifiable rather than guess.
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

// VerifyLayer streams a layer through sha256 and compares it against
// a digest a sidecar promised.
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
