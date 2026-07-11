package releases

// Bundling a release of liken: the artifacts and the document that
// every fleet, everywhere, upgrades from.
//
// The bundle is four artifacts. vmlinuz and the generic liken.cpio
// are the operating system, with no deployment inside; the liken
// binary is the toolkit that turns them into a deployment's bootable
// media without this repo or a build; systemd-bootx64.efi is the
// menu program that media boots (the systemd-boot domain explains
// why a stick needs one when installed machines don't). Because
// nothing here embeds a deployment, every digest is stable for a
// given source tree: publishable on a release page, and the same for
// everyone.
//
// That stability is what lets machines upgrade straight from the
// public channel. A deployment pins the release document's digest in
// its Cluster's spec.releases.catalog (the entry this bundle prints),
// its machines verify the document against the catalog and the
// artifacts against the document, and each machine supplies the one
// thing the release can't: its own deployment layer, carried forward
// from the slot it is running on. Nothing is composed or hosted per
// deployment.

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Bundle lays out a release: the named artifacts copied into
// <channel>/<version>/ beside a release.yaml naming each by sha256
// digest and size.
func Bundle(vmlinuz, image, cli, bootMenu, channelDir, version string, out io.Writer) error {
	dest := filepath.Join(channelDir, version)
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	// The artifacts keep canonical names in the channel whatever
	// their build-tree paths were, so the document and the URLs are
	// the same for every release.
	sources := []struct {
		src, name string
	}{
		{vmlinuz, "vmlinuz"},
		{image, "liken.cpio"},
		{cli, "liken"},
		{bootMenu, "systemd-bootx64.efi"},
	}
	document := fmt.Sprintf("apiVersion: liken.sh/v1alpha1\nkind: Release\nmetadata:\n  name: %s\nartifacts:\n", version)
	for _, s := range sources {
		dst := filepath.Join(dest, s.name)
		if err := copyFile(s.src, dst); err != nil {
			return err
		}
		digest, err := fileSHA256(dst)
		if err != nil {
			return err
		}
		info, err := os.Stat(dst)
		if err != nil {
			return err
		}
		document += fmt.Sprintf("  - name: %s\n    sha256: %s\n    size: %d\n", s.name, digest, info.Size())
	}

	// The document is generated from the copies in the channel, the
	// bytes that will actually be served, so the two can never
	// disagree.
	if err := os.WriteFile(filepath.Join(dest, "release.yaml"), []byte(document), 0o644); err != nil {
		return err
	}

	if err := report(dest, version, out); err != nil {
		return err
	}

	// The document's own digest is the root of the trust chain: a
	// person verifies a download against it, and a deployment commits
	// it to its Cluster so the fleet can verify what it fetches.
	digest, err := fileSHA256(filepath.Join(dest, "release.yaml"))
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\ncatalog entry for a Cluster's spec.releases.catalog:\n")
	fmt.Fprintf(out, "  - version: %s\n", version)
	fmt.Fprintf(out, "    digest: sha256:%s\n", digest)
	return nil
}

// report prints what landed in the channel.
func report(dest, version string, out io.Writer) error {
	entries, err := os.ReadDir(dest)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\npublished liken %s to %s:\n", version, dest)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "  %8s  %s\n", humanSize(info.Size()), e.Name())
	}
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func fileSHA256(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

// humanSize renders a byte count the way a person scans a listing,
// in the unit that keeps the number small.
func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0fM", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0fK", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}
