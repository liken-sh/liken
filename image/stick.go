package image

// The install stick: one bootable disk image per deployment.
//
// A stick is what turns a downloaded release and a deployment layer
// into running machines. Its disk image is a GPT with a single EFI
// system partition, and the partition holds two kinds of thing: the
// boot half (systemd-boot as the well-known \EFI\BOOT\BOOTX64.EFI
// that firmware runs from removable media, its menu configuration,
// and the files the menu entries boot) and the install payload the
// booted installer copies onto the machine's own disk.
//
// The menu is the deployment's machine list. Every entry boots the
// same kernel and the same two initramfs archives; what differs is
// one argument, liken.machine=<name>, so the operator standing at a
// machine picks its name and everything else follows. One stick
// serves the whole fleet, and reflashing between machines is never
// needed.
//
// The payload duplicates the OS files that also sit beside it on the
// stick (~160MB): the installer reads /usr/share/liken/release from
// its own initramfs and never looks back at the stick's filesystem.
// Teaching the installer to read the stick would save the space; it
// is deliberately not taken on, to keep the installer identical
// across the stick and the lab's direct-kernel path.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liken-sh/liken/disks"
	"github.com/liken-sh/liken/machine"
)

// bootMenuArtifact is systemd-boot's canonical name in a release.
const bootMenuArtifact = "systemd-bootx64.efi"

// Stick builds the install image: releaseDir is a downloaded release
// (artifacts beside their release.yaml), layerPath the deployment's
// layer, out the disk image to write. consoles adds console=
// arguments to every menu entry — and through the installer, to the
// machines' permanent boot entries — so the default (none) leaves
// hardware on its own screen, and the lab passes ttyS0.
func Stick(releaseDir, layerPath, out string, consoles []string, log io.Writer) error {
	document, release, err := verifiedRelease(releaseDir)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(release.Artifacts, func(a machine.ReleaseArtifact) bool {
		return a.Name == bootMenuArtifact
	}) {
		return fmt.Errorf("release %s carries no %s; it predates install sticks — use a newer release",
			release.Metadata.Name, bootMenuArtifact)
	}

	layer, err := os.ReadFile(layerPath)
	if err != nil {
		return fmt.Errorf("reading the deployment layer: %w", err)
	}
	machines, err := machineNames(layer)
	if err != nil {
		return err
	}

	// The payload spills to a temp file next to the image: it is
	// hundreds of megabytes, and its exact size is needed before the
	// image can be laid out.
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

	// Everything the partition will hold, with the loader files
	// generated up front so their sizes count too.
	loaderConf := loaderConfText()
	entries := map[string][]byte{}
	for _, name := range machines {
		entries["loader/entries/"+name+".conf"] = entryText(name, consoles)
	}

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
	// filesystem: a person with the stick already ran the toolkit to
	// make it, and the installer copies the slot's own copy from the
	// payload.
	espBytes := espSize(sizes)

	// The image: 1MiB of alignment before the partition, the ESP,
	// and the GPT's mirrored tail.
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

	// The filesystem, inside the partition's window of the image.
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
	for _, name := range machines {
		text := entries["loader/entries/"+name+".conf"]
		if err := w.WriteFile("loader/entries/"+name+".conf", strings.NewReader(string(text)), int64(len(text))); err != nil {
			return err
		}
	}
	if err := copyIn("vmlinuz", filepath.Join(releaseDir, "vmlinuz")); err != nil {
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
// the allocation tables and directories, a little slack, rounded to
// whole MiB, and never below FAT32's ~260MiB floor (the FAT type is
// determined by cluster count, so a smaller volume would not be
// FAT32 at all).
func espSize(contentBytes int64) int64 {
	size := contentBytes + contentBytes/10 + 8<<20
	size = (size + (1 << 20) - 1) &^ ((1 << 20) - 1)
	return max(size, 300<<20)
}

// loaderConfText is the menu's one setting: wait for a person,
// forever. An installer must never pick a machine by timeout — the
// whole point of the menu is that a human says which machine this is.
func loaderConfText() string {
	return `# The liken installer's menu. Each entry below installs one of this
# deployment's machines; pick the machine you are standing at. The
# "e" key edits an entry's options for one boot, if you ever need to.
timeout menu-force
`
}

// entryText is one machine's menu entry, in systemd-boot's
// Boot Loader Specification form. The three initrd lines concatenate
// in order — the same composition an installed machine gets from the
// two initrd= parameters in its boot entries, plus the installer's
// payload. The sort-key matters more than it looks: without one,
// systemd-boot orders entries the way it orders kernels, newest
// first, which put node-5 at the top of the menu; sort-keys sort
// ascending, so the machines read in their natural order.
func entryText(name string, consoles []string) []byte {
	options := []string{"rdinit=/liken", "liken.machine=" + name, "liken.install"}
	for _, c := range consoles {
		options = append(options, "console="+c)
	}
	return fmt.Appendf(nil, `# Boot this deployment's OS with the identity %q and install it
# onto this machine's own disks.
title install as %s
sort-key %s
linux /vmlinuz
initrd /boot.cpio
initrd /%s
initrd /payload.cpio
options %s
`, name, name, name, machine.LayerName, strings.Join(options, " "))
}
