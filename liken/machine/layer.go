package machine

// The deployment layer, deployment.cpio, is the part of the OS that
// belongs to one cluster. A boot slot holds it beside the release's
// public artifacts. The release document names those artifacts, but it
// cannot name the layer, because the layer is private to one
// cluster and is never downloaded. At each upgrade, machine-operator
// copies the layer from the active slot to the new one.
//
// The sidecar file, deployment.cpio.sha256, holds the layer's sha256
// so that each copy can be verified. The install image carries it.
// The installer and each upgrade verify the layer against it before
// and after the copy, and write the sidecar last. The sidecar uses the
// format of sha256sum, so a person at a rescue prompt can check a slot
// with `sha256sum -c deployment.cpio.sha256`.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

// LayerName is the file name of the deployment layer on install media
// and in every boot slot. Boot entries load it as the last initrd,
// after microcode.cpio and boot.cpio. The kernel unpacks the initrd
// archives in order, so a file in the layer replaces the file at the
// same path in boot.cpio.
const LayerName = "deployment.cpio"

// LayerSidecarName is the file name of the sidecar, in the directory
// of the layer.
const LayerSidecarName = LayerName + ".sha256"

// DigestLayer returns the hex sha256 of a layer. To verify a copy,
// pass a reader of the file on disk, not of the bytes that were
// written.
func DigestLayer(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("reading the layer: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func FormatLayerSidecar(digest string) []byte {
	return []byte(digest + "  " + LayerName + "\n")
}

// ParseLayerSidecar returns the digest in a sidecar file. It returns
// an error for anything but the exact line that FormatLayerSidecar
// writes, such as a truncated file or a sidecar for another file, and
// the caller must then treat the layer as unverified.
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
		return "", fmt.Errorf("the layer sidecar ends with %q, want %q", string(raw[64:]), string(want))
	}
	return digest, nil
}

func VerifyLayer(digest string, r io.Reader) error {
	got, err := DigestLayer(r)
	if err != nil {
		return err
	}
	if got != digest {
		return fmt.Errorf("the digest of %s is %s, want %s", LayerName, got, digest)
	}
	return nil
}
