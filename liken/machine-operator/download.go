package main

// The download that fetch.go runs. It writes one release into the
// inactive slot.
//
// A slot boots only when it has the release's artifacts and this
// machine's deployment layer. The layer is specific to the cluster, so
// no release contains it, and copyActiveLayer copies it from the slot
// the machine is running from.
//
// A download resumes by verification, not by byte ranges. Each run
// checks the files already in the slot against the release document
// and downloads only the files that fail. An interrupted run leaves
// either a `.partial` file, which no check reads, or a final file that
// passes or fails the check, so the next run finishes the job either
// way. The slot is FAT, which has no journal, so every file is written
// to a temp file, fsynced, and renamed into place.

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

// fetchRelease downloads the release document, checks it against the
// catalog's digest, removes the slot's old document, downloads each
// artifact that the slot does not already have, copies the layer, and
// writes the new document last. So a slot with release.yaml has every
// artifact and the layer, and init treats it as complete. It returns
// the number of artifacts it downloaded and their total size in bytes,
// counting an artifact only after it passes its check.
func fetchRelease(ctx context.Context, client *http.Client, fetch releaseFetch) (int, int64, error) {
	base := strings.TrimSuffix(fetch.source, "/") + "/" + fetch.version

	raw, err := fetchBytes(ctx, client, base+"/release.yaml")
	if err != nil {
		return 0, 0, fmt.Errorf("fetching the release document: %w", err)
	}

	// The artifact digests in the document are trusted only after the
	// document matches the catalog's digest.
	sum := sha256.Sum256(raw)
	if digest := "sha256:" + hex.EncodeToString(sum[:]); digest != fetch.digest {
		return 0, 0, fmt.Errorf("the release document's digest %s does not match the catalog's %s: %w", digest, fetch.digest, errCorrupt)
	}
	release, err := machine.ParseRelease(raw)
	if err != nil {
		return 0, 0, fmt.Errorf("parsing the release document: %v: %w", err, errCorrupt)
	}
	if release.Metadata.Name != fetch.version {
		return 0, 0, fmt.Errorf("the release document names version %s, not %s: %w", release.Metadata.Name, fetch.version, errCorrupt)
	}

	if err := removeStaleSlotDocument(fetch.slotDir, raw); err != nil {
		return 0, 0, fmt.Errorf("removing the slot's previous release document: %w", err)
	}

	fetched := 0
	downloaded := int64(0)
	for _, artifact := range release.Artifacts {
		dest := filepath.Join(fetch.slotDir, artifact.Name)
		if verifySlotFile(artifact, dest) == nil {
			continue
		}
		if err := fetchArtifact(ctx, client, base, artifact, dest); err != nil {
			return fetched, downloaded, err
		}
		fetched++
		downloaded += artifact.Size
	}

	if err := copyActiveLayer(fetch); err != nil {
		return fetched, downloaded, err
	}

	if err := writeDurably(filepath.Join(fetch.slotDir, "release.yaml"), raw); err != nil {
		return fetched, downloaded, fmt.Errorf("writing the release document to the slot: %w", err)
	}
	return fetched, downloaded, nil
}

// removeStaleSlotDocument removes the slot's release.yaml unless it
// already matches raw. While the artifacts change, the slot has no
// document, so init cannot start a proving boot of a slot with the files
// of two releases (armProvingBoot in init/proving.go checks the
// document and every artifact it lists). The removal reaches the disk
// before the first artifact is written, so a power cut during the
// download leaves a slot with no document, not one with the old
// document.
//
// On FAT, a file's directory entry is in the block device's buffers,
// and an fsync of the directory does not write them (see flushSlot in
// init/slotloader.go). So syncfs writes back the whole filesystem, and
// the fsync of the directory then flushes the drive's write cache.
func removeStaleSlotDocument(slotDir string, raw []byte) error {
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

// copyActiveLayer copies the running slot's deployment layer and its
// sidecar to the inactive slot. The running slot's sidecar was written
// from verified bytes, at install or by an earlier copy. When the layer
// does not match that sidecar, the running slot is damaged, and no
// retry or download can fix it. So the error wraps errLayer, and the
// fetcher reports Rejected and stops retrying until a person repairs or
// reinstalls the machine.
func copyActiveLayer(fetch releaseFetch) error {
	sidecar, err := os.ReadFile(filepath.Join(fetch.activeSlotDir, machine.LayerSidecarName))
	if err != nil {
		return fmt.Errorf("the running slot's deployment layer has no readable sidecar (%v), so repair or reinstall this machine: %w", err, errLayer)
	}
	digest, err := machine.ParseLayerSidecar(sidecar)
	if err != nil {
		return fmt.Errorf("the running slot's layer sidecar is damaged (%v), so repair or reinstall this machine: %w", err, errLayer)
	}
	verify := func(path string) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		return machine.VerifyLayer(digest, f)
	}
	source := filepath.Join(fetch.activeSlotDir, machine.LayerName)
	if err := verify(source); err != nil {
		return fmt.Errorf("the running slot's deployment layer does not match its sidecar (%v), so repair or reinstall this machine: %w", err, errLayer)
	}

	// A layer already in the inactive slot that matches the running
	// slot's sidecar is not copied again. A layer from an older install
	// fails the check and is replaced.
	dest := filepath.Join(fetch.slotDir, machine.LayerName)
	destSidecar := filepath.Join(fetch.slotDir, machine.LayerSidecarName)
	if verify(dest) != nil {
		f, err := os.Open(source)
		if err != nil {
			return fmt.Errorf("reading the running slot's layer: %w", err)
		}
		tmp, err := writePartial(dest, f)
		f.Close()
		if err != nil {
			return fmt.Errorf("copying %s to the inactive slot: %w", machine.LayerName, err)
		}
		if err := verify(tmp); err != nil {
			os.Remove(tmp)
			return fmt.Errorf("the copy of the layer in the inactive slot does not match the sidecar: %v: %w", err, errLayer)
		}
		if err := os.Rename(tmp, dest); err != nil {
			return err
		}
	}

	// The sidecar is written after the layer, so a matching sidecar
	// means the copy finished.
	if existing, err := os.ReadFile(destSidecar); err != nil || !bytes.Equal(existing, sidecar) {
		if err := writeDurably(destSidecar, sidecar); err != nil {
			return fmt.Errorf("copying %s to the inactive slot: %w", machine.LayerSidecarName, err)
		}
	}
	return nil
}

// fetchArtifact downloads one artifact to a temp file, fsyncs it,
// checks its digest and size, and only then renames it into place, so
// the final name never points at unverified bytes.
func fetchArtifact(ctx context.Context, client *http.Client, base string, artifact machine.ReleaseArtifact, dest string) error {
	resp, err := releases.Get(ctx, client, base+"/"+artifact.Name)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", artifact.Name, err)
	}
	defer resp.Body.Close()

	// Reading one byte past the declared size makes an oversized
	// artifact fail the check without filling the slot with the rest.
	tmp, err := writePartial(dest, io.LimitReader(resp.Body, artifact.Size+1))
	if err != nil {
		return fmt.Errorf("writing %s: %w", artifact.Name, err)
	}

	if err := verifySlotFile(artifact, tmp); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("%s from the server does not verify: %v: %w", artifact.Name, err, errCorrupt)
	}
	return os.Rename(tmp, dest)
}

// verifySlotFile checks the file at path against the artifact's digest
// and size. A missing file is an error.
func verifySlotFile(artifact machine.ReleaseArtifact, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return artifact.Verify(f)
}

// fetchBytes returns the body of a GET, cut at 1MiB, which is far more
// than a release.yaml needs.
func fetchBytes(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	resp, err := releases.Get(ctx, client, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// writePartial writes r to dest plus ".partial", fsyncs and closes it,
// and returns that path for the caller to rename. On failure it
// removes the file.
func writePartial(dest string, r io.Reader) (string, error) {
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

// writeDurably writes contents to a temp file, fsyncs it, and renames
// it to dest.
func writeDurably(dest string, contents []byte) error {
	tmp, err := writePartial(dest, bytes.NewReader(contents))
	if err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}
