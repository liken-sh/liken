package releases

// This file bundles a release of liken: the artifacts and the
// document that every fleet, everywhere, upgrades from.
//
// The bundle holds nine artifacts. vmlinuz, the system image
// (liken.sqfs, the OS as a read-only filesystem that a machine mounts
// as its root), and the boot archive (boot.cpio, the small initramfs
// that the boot loader stages) are the operating system, with no
// deployment inside. microcode.cpio is the CPU microcode early cpio:
// boot entries name it as their first initrd, ahead of everything,
// at the point where the kernel looks for microcode before it
// decompresses anything (the microcode domain explains the format).
// It is vendored with its own pin, so an OS update never recomposes
// it. The liken binary is the toolkit that turns the artifacts into
// a deployment's bootable media without this repo or a build.
// systemd-bootx64.efi is the menu program that media boots (the
// systemd-boot domain explains why a stick needs one when installed
// machines do not). grub-boot.img and grub-core.img are the two
// halves of the bootloader that BIOS machines carry (the grub domain
// explains why they need one when UEFI machines do not). These stay
// inert on a UEFI machine; on a BIOS machine, init writes their bytes
// into the MBR and the biosBoot partition. LICENSES.md is the
// third-party notices that the licensing domain assembles: several of
// the artifacts carry other projects' GPL- and LGPL-licensed
// binaries, and the license terms of those projects require the
// notices to travel with the bytes. So the notices are an artifact
// like any other, and ride the same channel, sticks, and slots the
// binaries do. Because nothing here embeds a deployment, every digest
// stays stable for a given source tree. This makes it publishable on
// a release page, and the same for everyone.
//
// That stability is what lets machines upgrade straight from the
// public channel. A deployment pins the release document's digest in
// its Cluster's spec.releases.catalog (the entry this bundle
// prints). Its machines verify the document against the catalog, and
// verify the artifacts against the document. Each machine supplies
// the one thing the release cannot: its own deployment layer,
// carried forward from the slot it is running on. Nothing is
// composed or hosted for one deployment specifically.

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
)

// Bundle lays out a release: it copies the named artifacts into
// <channel>/<version>/, beside a release.yaml that names each one by
// sha256 digest and size, and records which upstream components
// shipped. The version must fit liken's calendar grammar (the api
// package defines it). Bundle enforces this here, where versions are
// authored, so a malformed version never reaches a channel at all.
func Bundle(vmlinuz, systemImage, bootArchive, microcode, cli, bootMenu, grubBoot, grubCore, licenses, channelDir, version string, components []machine.ReleaseComponent, out io.Writer) error {
	if err := api.ValidVersion(version); err != nil {
		return err
	}
	dest := filepath.Join(channelDir, version)
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	// The artifacts keep canonical names in the channel, whatever
	// their build-tree paths were, so the document and the URLs stay
	// the same for every release.
	sources := []struct {
		src, name string
	}{
		{vmlinuz, "vmlinuz"},
		{systemImage, "liken.sqfs"},
		{bootArchive, "boot.cpio"},
		{microcode, "microcode.cpio"},
		{cli, "liken"},
		{bootMenu, "systemd-bootx64.efi"},
		{grubBoot, "grub-boot.img"},
		{grubCore, "grub-core.img"},
		{licenses, "LICENSES.md"},
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
	// version above is a calendar date, and deliberately says nothing
	// about contents. So the document is where a reader learns which
	// kernel or k3s this release carries.
	if len(components) > 0 {
		document += "components:\n"
		for _, c := range components {
			document += fmt.Sprintf("  - name: %s\n    version: %s\n", c.Name, c.Version)
		}
	}

	// Bundle generates the document from the copies in the channel,
	// the bytes that will actually be served, so the two can never
	// disagree.
	if err := os.WriteFile(filepath.Join(dest, "release.yaml"), []byte(document), 0o644); err != nil {
		return err
	}

	// The channel document at the root names the newest release
	// present, so a cluster can discover that something newer exists
	// without listing the channel (the machine package explains why
	// the document is advisory, and never trusted).
	latest, err := writeChannelDocument(channelDir)
	if err != nil {
		return err
	}

	if err := report(dest, version, out); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nchannel.yaml names the latest release: %s\n", latest)

	// The document's own digest is the root of the trust chain. A
	// person verifies a download against it. A deployment commits it
	// to its Cluster, so the fleet can verify what it fetches.
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
// the newest release present. It computes the newest from what is
// actually in the channel, not from the version just bundled, so
// re-bundling an old release never moves the channel backwards. The
// zero-padded calendar grammar makes plain string order match the
// version order (releases/versioning.md), so the maximum string is
// the latest version.
func writeChannelDocument(channelDir string) (string, error) {
	entries, err := os.ReadDir(channelDir)
	if err != nil {
		return "", err
	}
	latest := ""
	for _, e := range entries {
		if !e.IsDir() || api.ValidVersion(e.Name()) != nil {
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
