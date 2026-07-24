package image

// This file builds the install stick: one bootable disk image for
// each deployment.
//
// A stick turns a downloaded release and a deployment layer into
// running machines. Its disk image is a GPT with a single EFI system
// partition, and the partition holds two kinds of thing: the boot
// half (systemd-boot as the well-known \EFI\BOOT\BOOTX64.EFI that
// firmware runs from removable media, its menu configuration, and
// the files the menu entries boot) and the install payload that the
// booted installer copies onto the machine's own disk.
//
// The menu is the deployment's machine list. Every entry boots the
// same kernel and the same two initramfs archives. What differs is
// one argument, liken.machine=<name>, so the operator standing at a
// machine picks its name and everything else follows. One stick
// serves the whole fleet, so nobody ever needs to reflash it between
// machines.
//
// The payload duplicates the OS files that also sit beside it on the
// stick (about 160MB): the installer reads /usr/share/liken/release
// from its own initramfs and never reads the stick's filesystem
// again after that. Teaching the installer to read the stick would
// save this space, but this script deliberately does not do that, to
// keep the installer identical across the stick and the lab's
// direct-kernel path.

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liken-sh/liken/disks"
	"github.com/liken-sh/liken/machine"
)

// bootMenuArtifact is systemd-boot's canonical name in a release.
const bootMenuArtifact = "systemd-bootx64.efi"

// microcodeArtifact is the CPU microcode early cpio's canonical name
// in a release. Every boot's initrd list leads with it.
const microcodeArtifact = "microcode.cpio"

// Stick builds the install image. releaseDir is a downloaded release
// (artifacts beside their release.yaml), layerPath is the
// deployment's layer, and out is the disk image to write. consoles
// adds console= arguments to every menu entry, and through the
// installer, to the machines' permanent boot entries. The default
// (none) leaves hardware on its own screen, and the lab passes
// ttyS0.
func Stick(releaseDir, layerPath, out string, consoles []string, log io.Writer) error {
	document, release, err := verifiedRelease(releaseDir)
	if err != nil {
		return err
	}
	for _, required := range []string{bootMenuArtifact, microcodeArtifact} {
		if !slices.ContainsFunc(release.Artifacts, func(a machine.ReleaseArtifact) bool {
			return a.Name == required
		}) {
			return fmt.Errorf("release %s carries no %s; use a newer release",
				release.Metadata.Name, required)
		}
	}

	layer, err := os.ReadFile(layerPath)
	if err != nil {
		return fmt.Errorf("reading the deployment layer: %w", err)
	}
	machines, err := machineNames(layer)
	if err != nil {
		return err
	}

	// The payload spills to a temp file next to the image. It is
	// hundreds of megabytes, and its exact size is needed before the
	// build can lay out the image.
	payload, err := os.CreateTemp(filepath.Dir(out), ".payload-*")
	if err != nil {
		return err
	}
	defer os.Remove(payload.Name())
	defer payload.Close()
	if err := writePayload(payload, releaseDir, release, document, layer); err != nil {
		return err
	}
	payloadInfo, err := payload.Stat()
	if err != nil {
		return err
	}

	// This is everything the partition will hold. It generates the
	// loader files up front, so their sizes count too.
	loaderConf := loaderConfText()
	entries := map[string][]byte{}
	for _, name := range machines {
		entries["loader/entries/"+name+"-install.conf"] = entryText(name, false, consoles)
		entries["loader/entries/"+name+"-reinstall.conf"] = entryText(name, true, consoles)
	}
	entries["loader/entries/hardware-report.conf"] = reportEntryText(consoles)

	sizes := int64(len(loaderConf)) + int64(payloadInfo.Size()) + int64(len(layer))
	for _, e := range entries {
		sizes += int64(len(e))
	}
	for _, a := range release.Artifacts {
		if a.Name != "liken" {
			sizes += a.Size
		}
	}
	// The CLI is in the payload but not laid on the stick's
	// filesystem. A person with the stick already ran the toolkit to
	// make it, and the installer copies the slot's own copy from the
	// payload.
	espBytes := espSize(sizes)

	// This is the image: 1MiB of alignment before the partition, the
	// ESP, and the GPT's mirrored tail.
	totalBytes := int64(disks.PartitionAlignment)*disks.SectorSize + espBytes +
		int64(disks.ReservedLBAs+disks.PartitionAlignment)*disks.SectorSize
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Truncate(totalBytes); err != nil {
		return err
	}

	totalSectors := uint64(totalBytes) / disks.SectorSize
	firstLBA := uint64(disks.PartitionAlignment)
	lastLBA := firstLBA + uint64(espBytes)/disks.SectorSize - 1
	table := &disks.Table{DiskGUID: disks.RandomGUID()}
	table.Entries = append(table.Entries, disks.Entry{
		TypeGUID:   disks.EFISystemPartition,
		UniqueGUID: disks.RandomGUID(),
		FirstLBA:   firstLBA,
		LastLBA:    lastLBA,
		Name:       "liken:install",
	})
	chunks, err := disks.SerializeGPT(table, totalSectors)
	if err != nil {
		return err
	}
	for _, chunk := range chunks {
		if _, err := f.WriteAt(chunk.Data, int64(chunk.LBA)*disks.SectorSize); err != nil {
			return fmt.Errorf("writing the partition table: %w", err)
		}
	}

	// This is the filesystem, inside the partition's window of the
	// image.
	esp := disks.NewSection(f, int64(firstLBA)*disks.SectorSize, espBytes)
	if err := disks.FormatFAT32(esp, uint64(espBytes), "LIKEN-INST", 0x4C494B45); err != nil {
		return err
	}
	w, err := disks.NewFATWriter(esp)
	if err != nil {
		return err
	}
	for _, dir := range []string{"EFI", "EFI/BOOT", "loader", "loader/entries"} {
		if err := w.Mkdir(dir); err != nil {
			return err
		}
	}

	copyIn := func(path, src string) error {
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		info, err := in.Stat()
		if err != nil {
			return err
		}
		return w.WriteFile(path, in, info.Size())
	}
	if err := copyIn("EFI/BOOT/BOOTX64.EFI", filepath.Join(releaseDir, bootMenuArtifact)); err != nil {
		return err
	}
	if err := w.WriteFile("loader/loader.conf", strings.NewReader(loaderConf), int64(len(loaderConf))); err != nil {
		return err
	}
	for _, path := range slices.Sorted(maps.Keys(entries)) {
		text := entries[path]
		if err := w.WriteFile(path, strings.NewReader(string(text)), int64(len(text))); err != nil {
			return err
		}
	}
	if err := copyIn("vmlinuz", filepath.Join(releaseDir, "vmlinuz")); err != nil {
		return err
	}
	if err := copyIn(microcodeArtifact, filepath.Join(releaseDir, microcodeArtifact)); err != nil {
		return err
	}
	if err := copyIn("boot.cpio", filepath.Join(releaseDir, "boot.cpio")); err != nil {
		return err
	}
	if err := w.WriteFile(machine.LayerName, strings.NewReader(string(layer)), int64(len(layer))); err != nil {
		return err
	}
	if _, err := payload.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := w.WriteFile("payload.cpio", payload, payloadInfo.Size()); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}

	fmt.Fprintf(log, "install stick for liken %s: %d MB, %d machines on the menu\n",
		release.Metadata.Name, totalBytes/(1<<20), len(machines))
	fmt.Fprintf(log, "write it with: dd if=%s of=/dev/YOUR-STICK bs=4M oflag=direct status=progress\n", out)
	return nil
}

// espSize is the partition size for a given content size: room for
// the allocation tables and directories, a little slack, rounded up
// to a whole MiB, and never below FAT32's floor of about 260MiB (the
// FAT type depends on the cluster count, so a smaller volume would
// not be FAT32 at all).
func espSize(contentBytes int64) int64 {
	size := contentBytes + contentBytes/10 + 8<<20
	size = (size + (1 << 20) - 1) &^ ((1 << 20) - 1)
	return max(size, 300<<20)
}

// loaderConfText is the menu's one setting: wait for a person,
// forever. An installer must never pick a machine by timeout. The
// whole point of the menu is that a person says what this boot does.
func loaderConfText() string {
	return `# The liken installer's menu. Each machine has two entries: install
# it onto blank disks, or wipe and reinstall it over an existing liken
# install. The last entry is the hardware report, which writes a
# proposed manifest for the machine you are standing at and changes
# nothing on its disks. Pick the entry for what you mean to do. The
# "e" key edits an entry's options for one boot, if you ever need to.
timeout menu-force
`
}

// entryText is one machine's menu entry, in systemd-boot's Boot Loader
// Specification form. Each machine has two entries: install onto blank
// disks (liken.install), and wipe and reinstall over an existing liken
// install (liken.reinstall). The installer only ever claims a blank
// disk, so reinstall is the escape hatch for a disk liken itself wrote;
// picking the entry at the keyboard is the confirmation the reinstall
// needs.
//
// The four initrd lines concatenate in order, microcode first because
// the kernel scans the very start of its initrd for microcode. This is
// the same composition an installed machine gets from the three initrd=
// parameters in its boot entries, plus the installer's payload.
//
// The sort-key keeps a machine's two entries together and in order.
// Both keys begin with the machine name, so all of one machine's
// entries sort before the next machine's, and "install" sorts before
// "reinstall" within the pair. Without a sort-key, systemd-boot orders
// entries the way it orders kernels, newest first, which would scatter
// the pairs.
func entryText(name string, reinstall bool, consoles []string) []byte {
	word, action, sortSuffix := "liken.install", "install", "install"
	if reinstall {
		word, action, sortSuffix = "liken.reinstall", "wipe and reinstall", "reinstall"
	}
	options := []string{"rdinit=/liken", "liken.machine=" + name, word}
	for _, c := range consoles {
		options = append(options, "console="+c)
	}
	return fmt.Appendf(nil, `# Boot this deployment's OS with the identity %q and %s
# this machine's own disks.
title %s as %s
sort-key %s-%s
linux /vmlinuz
initrd /microcode.cpio
initrd /boot.cpio
initrd /%s
initrd /payload.cpio
options %s
`, name, action, action, name, name, sortSuffix, machine.LayerName, strings.Join(options, " "))
}

// reportEntryText is the stick-wide hardware report entry. It carries
// liken.report and no liken.machine=, because it describes the hardware
// in front of it, not a machine in the deployment. It boots the same
// kernel and initrd stack as the install entries, so the report reads
// its drivers from the same payload.
//
// The entry has no sort-key on purpose. systemd-boot shows every entry
// that carries a sort-key before every entry that does not, so the one
// report entry always lands after all the machine entries, wherever the
// machine names would otherwise sort it.
func reportEntryText(consoles []string) []byte {
	options := []string{"rdinit=/liken", "liken.report"}
	for _, c := range consoles {
		options = append(options, "console="+c)
	}
	return fmt.Appendf(nil, `# Describe this machine's hardware and write a proposed manifest to
# the stick as %s. This entry changes nothing on the machine's disks.
title liken hardware report
linux /vmlinuz
initrd /microcode.cpio
initrd /boot.cpio
initrd /%s
initrd /payload.cpio
options %s
`, "hardware-report.yaml", machine.LayerName, strings.Join(options, " "))
}
