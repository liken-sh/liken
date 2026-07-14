package releases

// Bundling a release of liken: the artifacts and the document that
// every fleet, everywhere, upgrades from.
//
// The bundle is seven artifacts. vmlinuz, the system image
// (liken.sqfs, the OS as a read-only filesystem a machine mounts as
// its root), and the boot archive (boot.cpio, the small initramfs
// the boot loader stages) are the operating system, with no
// deployment inside; the liken binary is the toolkit that turns them
// into a deployment's bootable media without this repo or a build;
// systemd-bootx64.efi is the menu program that media boots (the
// systemd-boot domain explains why a stick needs one when installed
// machines don't); grub-boot.img and grub-core.img are the two
// halves of the bootloader BIOS machines carry (the grub domain
// explains why they need one when UEFI machines don't) — inert
// passengers on a UEFI machine, and the bytes init writes into the
// MBR and the biosBoot partition on a BIOS one. Because nothing here
// embeds a deployment, every digest is stable for a given source
// tree: publishable on a release page, and the same for everyone.
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

	"github.com/liken-sh/liken/machine"
)

// Bundle lays out a release: the named artifacts copied into
// <channel>/<version>/ beside a release.yaml naming each by sha256
// digest and size, and recording which upstream components shipped.
// The version must fit liken's calendar grammar (the machine package
// defines it); enforcing that here, where versions are authored,
// means a malformed one never reaches a channel at all.
func Bundle(vmlinuz, systemImage, bootArchive, cli, bootMenu, grubBoot, grubCore, channelDir, version string, components []machine.ReleaseComponent, out io.Writer) error {
	if err := machine.ValidVersion(version); err != nil {
		return err
	}
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
		{systemImage, "liken.sqfs"},
		{bootArchive, "boot.cpio"},
		{cli, "liken"},
		{bootMenu, "systemd-bootx64.efi"},
		{grubBoot, "grub-boot.img"},
		{grubCore, "grub-core.img"},
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

	// The components record what shipped inside those artifacts. The
	// version above is a calendar date and deliberately says nothing
	// about contents, so the document is where a reader learns which
	// kernel or k3s this release carries.
	if len(components) > 0 {
		document += "components:\n"
		for _, c := range components {
			document += fmt.Sprintf("  - name: %s\n    version: %s\n", c.Name, c.Version)
		}
	}

	// The document is generated from the copies in the channel, the
	// bytes that will actually be served, so the two can never
	// disagree.
	if err := os.WriteFile(filepath.Join(dest, "release.yaml"), []byte(document), 0o644); err != nil {
		return err
	}

	// The channel document at the root announces the newest release
	// present, so a cluster can discover that something newer exists
	// without listing the channel (the machine package explains why
	// the document is advisory, never trusted).
	latest, err := writeChannelDocument(channelDir)
	if err != nil {
		return err
	}

	if err := report(dest, version, out); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nchannel.yaml names the latest release: %s\n", latest)

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

// writeChannelDocument rewrites the channel's root document to name
// the newest release present. The newest is computed from what is
// actually in the channel, not from the version just bundled, so
// re-bundling an old release never moves the channel backwards. The
// zero-padded calendar grammar makes plain string order the version
// order (releases/versioning.md), so the max is the latest.
func writeChannelDocument(channelDir string) (string, error) {
	entries, err := os.ReadDir(channelDir)
	if err != nil {
		return "", err
	}
	latest := ""
	for _, e := range entries {
		if !e.IsDir() || machine.ValidVersion(e.Name()) != nil {
			continue
		}
		if e.Name() > latest {
			latest = e.Name()
		}
	}
	document := fmt.Sprintf("apiVersion: liken.sh/v1alpha1\nkind: Channel\nmetadata:\n  name: liken\nlatest: %s\n", latest)
	return latest, os.WriteFile(filepath.Join(channelDir, "channel.yaml"), []byte(document), 0o644)
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
