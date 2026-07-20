package image

// This file builds install media: a public release and a deployment
// layer become the image a machine boots once, to install itself.
//
// An install boot is a small one. The installer is init itself, and
// it never needs the running system, only the manifests that say how
// to partition, and the payload to copy. So the install image is
// three cpio archives concatenated: the release's boot archive
// (boot.cpio: init and the early boot's modules), the deployment
// layer (the manifests whose storage specs drive the partitioning),
// and a wrapper carrying the release payload at
// /usr/share/liken/release. The kernel's initramfs unpacker
// processes concatenated archives in order into one filesystem, the
// same mechanism the machine's boot entries use to join their two
// halves from the slot.
//
// The payload is the slot layout, exactly: every artifact the release
// document lists, the document itself byte for byte, and the layer
// beside its sidecar. Carrying the document verbatim, rather than
// generating one, is what lets the installed machine verify later
// downloads against the same catalog digest the release was
// published under. The stick and the internet then vouch for the
// same bytes.
//
// Everything is verified before a single byte is packed. An artifact
// that fails its document here would fail it again in the installer,
// on a machine, where the only remedy is new media.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/liken-sh/liken/machine"
)

// payloadDir is the path where the wrapper archive carries the
// release. It is the path the installer reads (init's
// releasePayloadDir).
const payloadDir = "usr/share/liken/release"

// Media assembles install media from a release directory (a public
// bundle: artifacts beside their release.yaml) and a deployment
// layer, and writes the bootable image to out.
func Media(releaseDir, layerPath, out string, log io.Writer) error {
	document, release, err := verifiedRelease(releaseDir)
	if err != nil {
		return err
	}

	layer, err := os.ReadFile(layerPath)
	if err != nil {
		return fmt.Errorf("reading the deployment layer: %w", err)
	}

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()

	// This writes the boot archive first, then the layer, so the
	// layer's entries override at unpack.
	generic, err := os.Open(filepath.Join(releaseDir, "boot.cpio"))
	if err != nil {
		return err
	}
	_, err = io.Copy(f, generic)
	generic.Close()
	if err != nil {
		return err
	}
	if _, err := f.Write(layer); err != nil {
		return err
	}

	if err := writePayload(f, releaseDir, release, document, layer); err != nil {
		return err
	}

	info, err := f.Stat()
	if err != nil {
		return err
	}
	fmt.Fprintf(log, "install media for liken %s: %d MB (%d artifacts + the deployment layer)\n",
		release.Metadata.Name, info.Size()/(1<<20), len(release.Artifacts))
	return nil
}

// verifiedRelease reads a release directory's document and proves
// every listed artifact against it. This is the same check the fetch
// path performs, done before a single byte is packed, because an
// artifact that fails its document here would fail it again on a
// machine, where the only remedy is new media.
func verifiedRelease(releaseDir string) ([]byte, *machine.Release, error) {
	document, err := os.ReadFile(filepath.Join(releaseDir, "release.yaml"))
	if err != nil {
		return nil, nil, fmt.Errorf("reading the release document: %w", err)
	}
	release, err := machine.ParseRelease(document)
	if err != nil {
		return nil, nil, err
	}
	for _, artifact := range release.Artifacts {
		f, err := os.Open(filepath.Join(releaseDir, artifact.Name))
		if err != nil {
			return nil, nil, fmt.Errorf("the release is missing an artifact its document lists: %w", err)
		}
		err = artifact.Verify(f)
		f.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("the release does not match its own document: %w", err)
		}
	}
	return document, release, nil
}

// writePayload packs the installer's payload as a wrapper archive:
// the slot layout, exactly. It includes every artifact the document
// lists, the document itself byte for byte, and the layer beside the
// sidecar computed from it. The artifacts were verified before this
// function was called, and this function reads them again here. The
// document and the layer travel as the bytes already in hand.
func writePayload(w io.Writer, releaseDir string, release *machine.Release, document, layer []byte) error {
	sum := sha256.Sum256(layer)
	sidecar := machine.FormatLayerSidecar(hex.EncodeToString(sum[:]))

	payload := []struct {
		name string
		data []byte
	}{
		{"release.yaml", document},
		{machine.LayerName, layer},
		{machine.LayerSidecarName, sidecar},
	}
	for _, artifact := range release.Artifacts {
		data, err := os.ReadFile(filepath.Join(releaseDir, artifact.Name))
		if err != nil {
			return err
		}
		payload = append(payload, struct {
			name string
			data []byte
		}{artifact.Name, data})
	}

	a := newArchive(w)
	for _, d := range []string{"usr", "usr/share", "usr/share/liken", payloadDir} {
		if err := a.dir(d, 0o755); err != nil {
			return err
		}
	}
	for _, file := range payload {
		if err := a.file(filepath.Join(payloadDir, file.name), file.data, 0o644); err != nil {
			return err
		}
	}
	return a.close()
}
