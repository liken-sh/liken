package main

// The installer: the USB-stick boot that puts liken on its own disk.
//
// A machine booted with liken.install on its command line is running
// from external media — QEMU's -kernel flag in the lab, an installer
// stick or PXE on real hardware — and its job this boot is not to
// serve a cluster but to make external media unnecessary: verify the
// release payload it carries, copy it into system slot A, register
// both slots in the firmware's boot menu, and power off. Every boot
// after comes from the disk.
//
// The installer is liken itself, not a separate program: the same
// init, the same storage reconciliation (which is what claimed and
// formatted the slots moments earlier, on a fresh machine), the same
// manifest selection. Two things differ: the destination, and the
// ending. An install boot ends in a power-off because the install
// medium must not boot again; with QEMU's -kernel present, a reboot
// would just re-run the installer.
//
// Idempotence is what makes a crash safe. A power cut mid-install
// leaves claimed slots (claiming is resumable by name), half-copied
// files that fail verification and are copied again, and boot
// entries that are found by description and rewritten in place.
// Running the installer twice converges; there is no state to clean
// up first.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chrisguidry/liken/machine"
)

// installParam is the command-line flag that makes a boot an
// install. It is a flag rather than a separate image because the
// installer *is* the OS; whoever configures the bootloader decides
// which job this boot performs.
const installParam = "liken.install"

// releasePayloadDir is where the install image carries the release
// it installs: the exact artifacts, byte for byte, that the release's
// own document describes. image/build.sh assembles this as a second
// cpio concatenated onto the ordinary image; the kernel unpacks
// concatenated archives in order, the same mechanism early microcode
// updates use. A variable so tests can supply a payload of their own.
var releasePayloadDir = "/usr/share/liken/release"

// installToDisk performs the whole install against the slots that
// storage reconciliation just mounted. It returns rather than powers
// off so main can apply boot policy; on success the machine has
// nothing left to do but stop.
func installToDisk(machineName string) error {
	if machineName == "" {
		return fmt.Errorf("install: this machine has no name (liken.machine= or a manifest must supply one); boot entries carry identity, so an anonymous install would be wrong on every later boot")
	}

	// The slots must both exist before anything is copied. The design
	// depends on a fallback slot being registered from the start, so
	// an install that can only register one slot must not proceed.
	parts := discoverPartitions()
	slotA, err := findSlotPartition(parts, machine.SystemARole)
	if err != nil {
		return err
	}
	slotB, err := findSlotPartition(parts, machine.SystemBRole)
	if err != nil {
		return err
	}

	// The payload is verified before a single byte is copied: the
	// release document names each artifact's digest, and the copies
	// this image carries must match it exactly.
	raw, err := os.ReadFile(filepath.Join(releasePayloadDir, "release.yaml"))
	if err != nil {
		return fmt.Errorf("install: reading the release document: %w", err)
	}
	release, err := machine.ParseRelease(raw)
	if err != nil {
		return fmt.Errorf("install: %w", err)
	}
	fmt.Printf("liken: install: release %s, %d artifacts\n", release.Metadata.Name, len(release.Artifacts))

	slotMount := roleMounts[machine.SystemARole].path
	for _, artifact := range release.Artifacts {
		source := filepath.Join(releasePayloadDir, artifact.Name)
		if err := verifyFile(artifact, source); err != nil {
			return fmt.Errorf("install: the payload this image carries doesn't match its own release document: %w", err)
		}
		dest := filepath.Join(slotMount, artifact.Name)
		if err := copyDurably(source, dest); err != nil {
			return fmt.Errorf("install: copying %s: %w", artifact.Name, err)
		}
		// Verify what was written, not what was meant: the copy is
		// re-read from the slot and hashed again, so a torn or
		// corrupted write is caught now rather than on some later
		// boot.
		if err := verifyFile(artifact, dest); err != nil {
			return fmt.Errorf("install: the copy on the slot doesn't verify: %w", err)
		}
		fmt.Printf("liken: install: %s verified and installed (%d bytes)\n", artifact.Name, artifact.Size)
	}

	// Register both slots with the firmware. Slot B's entry points
	// at a file that doesn't exist yet, because its slot stays empty
	// until the first upgrade fills it. That's fine: a firmware that
	// can't load an entry's file moves on down BootOrder.
	entryA, err := writeSlotBootEntry(efiVarsDir, "liken slot A", "A", slotA, machineName)
	if err != nil {
		return err
	}
	entryB, err := writeSlotBootEntry(efiVarsDir, "liken slot B", "B", slotB, machineName)
	if err != nil {
		return err
	}

	// BootOrder: our slots first, A then B (B only matters when a
	// proving boot armed it via BootNext, or when A is broken), then
	// whatever the firmware already had, in its old order. The
	// firmware's own entries (setup menus, shells) stay reachable,
	// just never preferred.
	order := []uint16{entryA, entryB}
	for _, n := range readBootOrder(efiVarsDir) {
		if n != entryA && n != entryB {
			order = append(order, n)
		}
	}
	if err := writeBootOrder(efiVarsDir, order); err != nil {
		return fmt.Errorf("install: writing BootOrder: %w", err)
	}

	fmt.Printf("liken: install: boot entries %s and %s written; BootOrder prefers slot A\n",
		bootEntryID(entryA), bootEntryID(entryB))
	return nil
}

// findSlotPartition locates one slot by the name written on it at
// claim time, and reads its GPT identity: the unique GUID a boot
// entry uses to pin the partition regardless of device position.
func findSlotPartition(parts []partition, role machine.StorageRoleName) (*slotPartition, error) {
	declared := machine.DeclaredRole{Name: role}
	for _, p := range parts {
		if p.partName != declared.PartitionName() {
			continue
		}
		device := devRoot + "/" + p.disk
		disk := diskByPath(device)
		if disk == nil {
			return nil, fmt.Errorf("install: %s found on %s but the disk is not in the inventory", role, device)
		}
		f, err := os.Open(device)
		if err != nil {
			return nil, err
		}
		table, err := readGPT(f, disk.SizeBytes/sectorSize)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("install: reading %s's partition table: %w", device, err)
		}
		for _, entry := range table.entries {
			if entry.name == declared.PartitionName() {
				return &slotPartition{
					number:   partitionNumber(p),
					firstLBA: entry.firstLBA,
					lastLBA:  entry.lastLBA,
					guid:     entry.uniqueGUID,
				}, nil
			}
		}
		return nil, fmt.Errorf("install: %s appears in sysfs but not in %s's table; refusing to guess", role, device)
	}
	return nil, fmt.Errorf("install: no partition carries %s; the manifest must declare both system slots before a machine can install itself", declared.PartitionName())
}

type slotPartition struct {
	number   uint32
	firstLBA uint64
	lastLBA  uint64
	guid     [16]byte
}

// partitionNumber recovers the partition's index from the kernel's
// node name: the suffix after the disk's name (vdc1 → 1), with the
// "p" separator NVMe-style names insert (nvme0n1p2 → 2). The kernel
// numbers partitions by their slot in the GPT entry array, starting
// at 1.
func partitionNumber(p partition) uint32 {
	suffix := strings.TrimPrefix(p.name, p.disk)
	suffix = strings.TrimPrefix(suffix, "p")
	var n uint32
	fmt.Sscanf(suffix, "%d", &n)
	return n
}

// writeSlotBootEntry writes one slot's firmware entry: boot \vmlinuz
// from this partition, with a command line assembled from scratch so
// that every argument is deliberate and none is inherited by
// accident:
//
//	console=...      copied from this boot, so the installed system
//	                 keeps using whatever console its operator wired
//	rdinit=/liken    run our program as PID 1
//	initrd=          \liken.cpio, next to the kernel; the EFI stub
//	                 loads the initramfs from the same filesystem it
//	                 loaded the kernel from
//	liken.machine=   identity, the bootloader's channel; the entry
//	                 inherits it from the installer boot
//	liken.slot=      which slot this entry boots, so a running system
//	                 knows which half of blue-green it is on
//	panic=10         reboot ten seconds after a kernel panic, instead
//	                 of hanging forever. Upgrades depend on this: a
//	                 panicking trial slot must reset so the firmware's
//	                 consumed BootNext can fall back to the proven one
func writeSlotBootEntry(dir, description, slot string, part *slotPartition, machineName string) (uint16, error) {
	args := consoleArgs()
	args = append(args,
		"rdinit=/liken",
		`initrd=\liken.cpio`,
		"liken.machine="+machineName,
		"liken.slot="+slot,
		"panic=10",
	)
	option := loadOption{
		attributes:  loadOptionActive,
		description: description,
		hardDrive: &hardDriveNode{
			partitionNumber: part.number,
			firstLBA:        part.firstLBA,
			sectors:         part.lastLBA - part.firstLBA + 1,
			partitionGUID:   part.guid,
		},
		filePath: `\vmlinuz`,
		// The EFI stub reads its command line as UTF-16, the
		// firmware's native string type.
		optionalData: encodeUTF16Z(strings.Join(args, " ")),
	}
	number, err := setBootEntry(dir, option)
	if err != nil {
		return 0, fmt.Errorf("install: writing the %s entry: %w", description, err)
	}
	return number, nil
}

// consoleArgs copies every console= argument from the running
// command line: the installer was told where this machine's console
// is, and the installed system should keep using the same one.
func consoleArgs() []string {
	raw, err := os.ReadFile(cmdlinePath)
	if err != nil {
		return nil
	}
	var consoles []string
	for _, field := range strings.Fields(string(raw)) {
		if strings.HasPrefix(field, "console=") {
			consoles = append(consoles, field)
		}
	}
	return consoles
}

// verifyFile checks one file on disk against its release artifact.
func verifyFile(artifact machine.ReleaseArtifact, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return artifact.Verify(f)
}

// copyDurably copies through a temporary name, fsyncs, and renames,
// so the slot never holds a file that looks final but isn't. FAT has
// no journal, so durability here is purely discipline: without the
// explicit sync before the rename, the page cache may still hold the
// file's bytes when the rename lands, and a power cut then leaves a
// final-looking file with incomplete contents.
func copyDurably(source, dest string) error {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp := dest + ".partial"
	dst, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := dst.ReadFrom(src); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}
