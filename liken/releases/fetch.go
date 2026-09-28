package releases

// This file downloads a release from a channel: the workstation half
// of the trust chain the machines already walk.
//
// A machine's operator downloads releases onto a boot slot. This
// fetch downloads the same releases onto an operator's workstation,
// where they become install media. The verification discipline is
// identical, because the threat is identical: the bytes crossed a
// network, and nothing about the transport is trusted. Fetch
// downloads the document first, and, when the caller pins a digest,
// checks it against that digest before a single artifact moves.
// Fetch verifies every artifact against the document before the
// artifact takes its final name. The document itself lands last, so
// a directory holding release.yaml is a directory whose artifacts
// were complete when release.yaml was written. This last property
// also makes the download resumable and safe to cache: a rerun
// verifies whatever already landed and fetches only what fails, so
// an interrupted download converges instead of staying half-finished
// under a name that looks complete.
//
// The digest pin is optional here and required on machines, and the
// difference is who holds the catalog. A machine always has its
// cluster's spec.releases.catalog to vouch for the document. A
// workstation composing a deployment's first media has no cluster
// yet, so the pin is offered (paste it from the release's published
// catalog entry) rather than required. What is never optional is the
// inner chain: no artifact escapes verification against the document
// that names it.

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/liken-sh/liken/machine"
)

// Fetch downloads one release from a channel's source URL into
// <channelDir>/<version>/, the same layout that Bundle produces and
// that the media and stick builders consume. The version "latest"
// resolves through the channel's advisory document. digest, when not
// empty, pins the release document's own bytes ("sha256:<hex>", the
// catalog-entry form).
func Fetch(source, version, digest, channelDir string, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	source = strings.TrimSuffix(source, "/")

	if version == "latest" {
		resolved, err := resolveLatest(source)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "the channel's latest release is %s\n", resolved)
		version = resolved
	}

	base := source + "/" + version
	raw, err := fetchDocument(base + "/release.yaml")
	if err != nil {
		return fmt.Errorf("fetching the release document: %w", err)
	}

	// This is the trust chain's first link, when the caller holds one:
	// the document's bytes must hash to exactly what the catalog entry
	// promised. Until that holds true, nothing the document says
	// counts.
	if digest != "" {
		if got := fmt.Sprintf("sha256:%x", sha256.Sum256(raw)); got != digest {
			return fmt.Errorf("the release document's digest %s does not match the pinned %s", got, digest)
		}
	}
	release, err := machine.ParseRelease(raw)
	if err != nil {
		return fmt.Errorf("the release document does not parse: %w", err)
	}
	if release.Metadata.Name != version {
		return fmt.Errorf("the release document names version %s, not %s", release.Metadata.Name, version)
	}

	dest := filepath.Join(channelDir, version)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	fetched := 0
	for _, artifact := range release.Artifacts {
		path := filepath.Join(dest, artifact.Name)
		if verifyFile(artifact, path) == nil {
			continue // already here, byte for byte, from an earlier run
		}
		fmt.Fprintf(out, "  fetching %s (%s)\n", artifact.Name, humanSize(artifact.Size))
		if err := fetchArtifact(base, artifact, path); err != nil {
			return err
		}
		fetched++
	}

	// The document lands after every artifact it describes. This makes
	// the directory self-describing: it records which release it
	// holds, byte for byte, without asking the network again.
	if err := os.WriteFile(filepath.Join(dest, "release.yaml"), raw, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: %d fetched, the rest already verified in place\n", version, fetched)
	return nil
}

// resolveLatest asks the channel's root document what the newest
// published release is. The document is advisory, outside the trust
// chain, which is exactly right here: it only chooses which version
// to fetch, and Fetch still verifies everything fetched against that
// version's own document.
func resolveLatest(source string) (string, error) {
	raw, err := fetchDocument(source + "/channel.yaml")
	if err != nil {
		return "", fmt.Errorf("fetching the channel document: %w", err)
	}
	channel, err := machine.ParseChannel(raw)
	if err != nil {
		return "", fmt.Errorf("the channel document does not parse: %w", err)
	}
	return channel.Latest, nil
}

// fetchArtifact streams one artifact into the channel directory: it
// downloads to a .partial name, verifies against the document, then
// renames the file. This verify-before-rename order means a
// final-looking name never points at unverified bytes, which is what
// lets reruns trust whatever they find already in place.
func fetchArtifact(base string, artifact machine.ReleaseArtifact, dest string) error {
	resp, err := http.Get(base + "/" + artifact.Name)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", artifact.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching %s: the server answered %s", artifact.Name, resp.Status)
	}

	tmp := dest + ".partial"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	// The size cap catches an artifact that runs past its declared
	// size, without downloading the rest of it.
	_, err = io.Copy(f, io.LimitReader(resp.Body, artifact.Size+1))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("writing %s: %w", artifact.Name, err)
	}

	if err := verifyFile(artifact, tmp); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("%s from the server does not verify: %w", artifact.Name, err)
	}
	return os.Rename(tmp, dest)
}

// verifyFile checks one downloaded file against its artifact's digest
// and size. It returns an error for any reason the check fails,
// including that the file does not exist, the common case on a first
// run.
func verifyFile(artifact machine.ReleaseArtifact, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return artifact.Verify(f)
}

// fetchDocument GETs a small document whole. The 1MiB cap is far
// larger than any reasonable release or channel document, and small
// enough to hold in memory without concern.
func fetchDocument(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the server answered %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
