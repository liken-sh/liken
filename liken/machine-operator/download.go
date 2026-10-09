package main

// The download: what one run of the fetcher writes to the inactive
// slot (fetch.go runs it).
//
// A complete fetch leaves a bootable slot, and this takes more than
// downloading the release. The public artifacts are downloaded and
// verified against the document. Then the machine's own deployment
// layer is carried over from the slot it is running on (carryLayer),
// because the layer never travels the network, and no release can
// supply it.
//
// Downloads resume through re-verification, not through byte
// ranges. Each run first verifies whatever the slot already holds
// against the release document, and fetches only what fails
// verification. A torn download, from a power cut or a killed
// server, leaves either a .partial file, which no verification ever
// counts, or a final file that either verifies or does not. The next
// run converges either way. FAT has no journal, so every file lands
// the way the installer's copies do: temp file, fsync, rename. The
// function re-reads and verifies the file after writing it, because
// bytes sitting in the page cache are not durable until they are
// synced and read back.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/releases"
)

// fetchRelease runs one complete pass. It fetches and checks the
// release document, removes the slot's old document, verifies or
// fetches each artifact, and writes the new document to the slot
// last. This order means a slot carrying release.yaml is a slot whose
// artifacts were complete when the document was written, and that no
// writer has changed since. fetchRelease returns how many
// artifacts it actually downloaded, and how many bytes those
// artifacts hold. Zero is the idempotent case, where everything was
// already verified in place. The byte total counts an artifact only
// after the artifact lands and verifies, so a torn file adds nothing
// until the run that completes it.
func fetchRelease(ctx context.Context, client *http.Client, ask fetchAsk) (int, int64, error) {
	base := strings.TrimSuffix(ask.source, "/") + "/" + ask.version

	raw, err := fetchBytes(ctx, client, base+"/release.yaml")
	if err != nil {
		return 0, 0, fmt.Errorf("fetching the release document: %w", err)
	}

	// The first check in the trust chain: the document's bytes must
	// hash to exactly what the catalog promised. Until that check
	// passes, nothing the document says can be trusted.
	sum := sha256.Sum256(raw)
	if digest := "sha256:" + hex.EncodeToString(sum[:]); digest != ask.digest {
		return 0, 0, fmt.Errorf("the release document's digest %s does not match the catalog's %s: %w", digest, ask.digest, errCorrupt)
	}
	release, err := machine.ParseRelease(raw)
	if err != nil {
		return 0, 0, fmt.Errorf("the release document does not parse: %v: %w", err, errCorrupt)
	}
	if release.Metadata.Name != ask.version {
		return 0, 0, fmt.Errorf("the release document names version %s, not %s: %w", release.Metadata.Name, ask.version, errCorrupt)
	}

	if err := withdrawSlotDocument(ask.slotDir, raw); err != nil {
		return 0, 0, fmt.Errorf("removing the slot's previous release document: %w", err)
	}

	fetched := 0
	downloaded := int64(0)
	for _, artifact := range release.Artifacts {
		dest := filepath.Join(ask.slotDir, artifact.Name)
		if verifySlotFile(artifact, dest) == nil {
			continue // already here from an earlier, interrupted run
		}
		if err := fetchArtifact(ctx, client, base, artifact, dest); err != nil {
			return fetched, downloaded, err
		}
		fetched++
		downloaded += artifact.Size
	}

	// The deployment layer is the one file the release cannot
	// supply. It belongs to this cluster alone, so the machine
	// carries it forward from the slot it is running on. This step
	// runs between the artifacts and the document deliberately: a
	// slot with release.yaml is bootable, and a slot without its
	// layer is not.
	if err := carryLayer(ask); err != nil {
		return fetched, downloaded, err
	}

	// The document lands after the artifacts it describes, written
	// durably. This makes the slot self-describing: it records
	// which release it holds, byte for byte, without asking the
	// network.
	if err := writeDurably(filepath.Join(ask.slotDir, "release.yaml"), raw); err != nil {
		return fetched, downloaded, fmt.Errorf("writing the release document to the slot: %w", err)
	}
	return fetched, downloaded, nil
}

// withdrawSlotDocument removes the slot's release document unless it
// is already the document of this release. The slot then claims no
// release while its files change, so init cannot arm a trial of a
// slot that holds part of one release and part of another
// (armProvingBoot in init/proving.go checks the document and every
// artifact it names). The removal reaches the disk before the first
// artifact is written, so a power cut in the middle of the download
// leaves a slot with no document, not one with the old document.
//
// A slot is FAT, where a file's directory entry lives in buffers of
// the block device, and an fsync of the directory does not write them
// (flushSlot in init/slotloader.go). So syncfs writes the slot's
// filesystem back, buffers included, and the fsync of the directory
// then empties the drive's write cache.
func withdrawSlotDocument(slotDir string, raw []byte) error {
	path := filepath.Join(slotDir, "release.yaml")
	existing, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) || bytes.Equal(existing, raw) {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	dir, err := os.Open(slotDir)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := unix.Syncfs(int(dir.Fd())); err != nil {
		return err
	}
	return dir.Sync()
}

// carryLayer copies the running slot's deployment layer and
// sidecar to the inactive slot. The active slot is the source of
// truth. Its sidecar was written from verified bytes at install, or
// by the carry that filled it. So a layer that fails to verify
// against the active sidecar means the running slot itself is
// damaged, a condition that no retry and no download can repair.
// This is why the fetcher holds it the way it holds corruption. The
// remedy belongs to a person: repair or reinstall the machine.
func carryLayer(ask fetchAsk) error {
	sidecar, err := os.ReadFile(filepath.Join(ask.activeSlotDir, machine.LayerSidecarName))
	if err != nil {
		return fmt.Errorf("the running slot's deployment layer cannot be vouched for (%v); repair or reinstall this machine: %w", err, errLayer)
	}
	digest, err := machine.ParseLayerSidecar(sidecar)
	if err != nil {
		return fmt.Errorf("the running slot's layer sidecar is damaged (%v); repair or reinstall this machine: %w", err, errLayer)
	}
	verify := func(path string) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		return machine.VerifyLayer(digest, f)
	}
	source := filepath.Join(ask.activeSlotDir, machine.LayerName)
	if err := verify(source); err != nil {
		return fmt.Errorf("the running slot's deployment layer does not verify (%v); repair or reinstall this machine: %w", err, errLayer)
	}

	// This resumes the same way the artifacts do. A layer already
	// carried, with a sidecar matching the active one, needs nothing
	// more. A layer from some older install fails this check and
	// gets replaced. A carry that died between writing the layer and
	// writing its sidecar resumes by rewriting only the sidecar.
	dest := filepath.Join(ask.slotDir, machine.LayerName)
	destSidecar := filepath.Join(ask.slotDir, machine.LayerSidecarName)
	if verify(dest) != nil {
		f, err := os.Open(source)
		if err != nil {
			return fmt.Errorf("reading the running slot's layer: %w", err)
		}
		tmp, err := spillDurably(dest, f)
		f.Close()
		if err != nil {
			return fmt.Errorf("carrying %s: %w", machine.LayerName, err)
		}
		if err := verify(tmp); err != nil {
			os.Remove(tmp)
			return fmt.Errorf("the carried layer does not verify: %v: %w", err, errLayer)
		}
		if err := os.Rename(tmp, dest); err != nil {
			return err
		}
	}

	// The sidecar lands last, written durably. A slot whose sidecar
	// matches its layer is a slot whose carry completed.
	if existing, err := os.ReadFile(destSidecar); err != nil || !bytes.Equal(existing, sidecar) {
		if err := writeDurably(destSidecar, sidecar); err != nil {
			return fmt.Errorf("carrying %s: %w", machine.LayerSidecarName, err)
		}
	}
	return nil
}

// fetchArtifact streams one artifact onto the slot: temp file,
// fsync, verify the durable bytes by re-reading them, then rename
// into place. Verifying before renaming means a final-looking file
// name never points at unverified bytes.
func fetchArtifact(ctx context.Context, client *http.Client, base string, artifact machine.ReleaseArtifact, dest string) error {
	resp, err := releases.Get(ctx, client, base+"/"+artifact.Name)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", artifact.Name, err)
	}
	defer resp.Body.Close()

	// The size cap protects the slot. An artifact that runs past its
	// declared size is already wrong, and there is no reason to
	// fill a 512Mi filesystem with the rest of it before finding
	// that out.
	tmp, err := spillDurably(dest, io.LimitReader(resp.Body, artifact.Size+1))
	if err != nil {
		return fmt.Errorf("writing %s: %w", artifact.Name, err)
	}

	if err := verifySlotFile(artifact, tmp); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("%s from the server does not verify: %v: %w", artifact.Name, err, errCorrupt)
	}
	return os.Rename(tmp, dest)
}

// verifySlotFile checks one file on the slot against its
// artifact's digest and size. It returns an error for any reason
// the file fails, including that the file does not exist, which is
// the common case on a first run.
func verifySlotFile(artifact machine.ReleaseArtifact, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return artifact.Verify(f)
}

// fetchBytes reads a small document whole with an HTTP GET. The
// 1MiB limit is far larger than any reasonable release.yaml, and
// small enough to read into memory without concern.
func fetchBytes(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	resp, err := releases.Get(ctx, client, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// spillDurably writes a stream next to its destination with the
// same steps the installer applies to file copies: write to a
// .partial temp file, fsync, close. On any failure, the function
// removes the temp file, and nothing further sees it. On success,
// the function returns the temp path for the caller to finish: the
// caller either verifies first and then renames (fetchArtifact), or
// renames immediately (writeDurably).
func spillDurably(dest string, r io.Reader) (string, error) {
	tmp := dest + ".partial"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, r)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// writeDurably writes bytes already in memory with the same
// steps: temp file, fsync, rename.
func writeDurably(dest string, contents []byte) error {
	tmp, err := spillDurably(dest, bytes.NewReader(contents))
	if err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}
