package main

// The installer: the USB-stick boot that puts liken on its own disk.
//
// A machine booted with liken.install on its command line is running
// from external media: QEMU's -kernel flag in the lab, or an
// installer stick or PXE on real hardware. Its job this boot is not
// to serve a cluster but to make external media unnecessary. It
// verifies the release payload it carries, copies it into system
// slot A, registers both slots with whatever governs booting
// (firmware boot entries on a UEFI machine, liken's own GRUB on a
// machine that declares the biosBoot and bootHome roles), and powers
// off. Every boot after this one comes from the disk.
//
// The installer is liken itself, not a separate program. It uses the
// same init, the same storage reconciliation (which claimed and
// formatted the slots moments earlier, on a fresh machine), and the
// same manifest selection. Two things differ: the destination, and
// the ending. An install boot ends in a power-off because the
// install medium must not boot again. With QEMU's -kernel flag
// present, a reboot would just run the installer again.
//
// Idempotence is what makes a crash safe. A power cut in the middle
// of an install leaves claimed slots (claiming is resumable by
// name), half-copied files that fail verification and are copied
// again, and boot entries that are found by description and
// rewritten in place. Running the installer twice converges to the
// same result; there is no state to clean up first.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/liken-sh/liken/disks"
	"github.com/liken-sh/liken/machine"
)

// installParam is the command-line flag that makes a boot an
// install. It is a flag rather than a separate image, because the
// installer is the OS. Whoever configures the bootloader decides
// which job this boot performs.
const installParam = "liken.install"

// releasePayloadDir is where the install image carries the release
// it installs: the artifacts that the release document lists, byte
// for byte, plus the deployment layer and its sidecar. image/media.go
// assembles this as a wrapper cpio archive, concatenated onto the
// composed system. The kernel unpacks concatenated archives in
// order, the same mechanism that early microcode updates use. This
// is a variable, so tests can supply a payload of their own.
var releasePayloadDir = "/usr/share/liken/release"

// installToDisk performs the whole install against the slots that
// storage reconciliation just mounted. It returns rather than powers
// off, so main can apply boot policy. On success, the machine has
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

	// This code verifies the payload before it copies a single byte.
	// The release document names each artifact's digest, and the
	// copies that this image carries must match it exactly.
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
		// This verifies what was written, not what was meant to be
		// written. The copy is re-read from the slot and hashed
		// again, so a torn or corrupted write is caught now rather
		// than on a later boot.
		if err := verifyFile(artifact, dest); err != nil {
			return fmt.Errorf("install: the copy on the slot doesn't verify: %w", err)
		}
		fmt.Printf("liken: install: %s verified and installed (%d bytes)\n", artifact.Name, artifact.Size)
	}

	// The deployment layer travels beside the listed artifacts, not
	// among them. The release document is the public one, and the
	// layer belongs to this deployment alone, vouched for by its
	// sidecar. The same discipline applies: verify the payload's
	// copy, copy it durably, and verify what landed. The sidecar is
	// written last, so a slot with a sidecar is a slot whose layer
	// was complete when it was written.
	if err := installLayer(slotMount); err != nil {
		return err
	}

	// The actuator half: register the slots with whatever will hold
	// this machine's boot choices. A UEFI machine gets firmware boot
	// entries. A machine whose spec declares the GRUB roles gets its
	// own bootloader written. Both at once is valid: a disk prepared
	// under UEFI can carry its GRUB for a BIOS life, and the lab's
	// dual-firmware disks do exactly that. But an install that no
	// firmware could ever boot is refused, before the power-off makes
	// it permanent.
	actuators := 0
	if firmwareIsUEFI() {
		if err := installBootEntries(slotA, slotB, machineName); err != nil {
			return err
		}
		actuators++
	}
	if hasPartition(parts, machine.BIOSBootRole) {
		if err := installGRUB(parts, machineName, slotMount); err != nil {
			return err
		}
		actuators++
	}
	if actuators == 0 {
		return fmt.Errorf("install: this machine's firmware holds no boot variables (BIOS) and its spec declares no biosBoot/bootHome roles for GRUB; there is nothing that could boot the installed disk")
	}
	return nil
}

// installBootEntries registers both slots with UEFI firmware. Slot
// B's entry points at files that do not exist yet, because its slot
// stays empty until the first upgrade fills it. This is fine: a
// firmware that cannot load an entry's file moves on to the next
// entry in BootOrder.
func installBootEntries(slotA, slotB *slotPartition, machineName string) error {
	entryA, err := writeSlotBootEntry(efiVarsDir, "liken slot A", "A", slotA, machineName)
	if err != nil {
		return err
	}
	entryB, err := writeSlotBootEntry(efiVarsDir, "liken slot B", "B", slotB, machineName)
	if err != nil {
		return err
	}

	// BootOrder: this machine's own slots come first, A then B (B
	// matters only when a proving boot armed it via BootNext, or when
	// A is broken). After them comes whatever the firmware already
	// had, in its previous order. The firmware's own entries (setup
	// menus, shells) stay reachable; they are just never preferred.
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

// installGRUB writes the bootloader for a machine that boots
// BIOS-style: the patched boot chain into the MBR and the biosBoot
// partition (grubinstall.go handles the arithmetic), and the config
// and environment block onto the boot home. The GRUB artifacts come
// from the slot that the release was just verified onto. They
// arrived through the same verify-copy-verify pipeline as everything
// else. Like the rest of the installer, this converges on re-run:
// every write puts down the same bytes.
func installGRUB(parts []partition, machineName, slotMount string) error {
	biosBoot, err := findSlotPartition(parts, machine.BIOSBootRole)
	if err != nil {
		return err
	}
	diskDev, err := diskDevice(parts, machine.BIOSBootRole)
	if err != nil {
		return err
	}

	bootImg, err := os.ReadFile(filepath.Join(slotMount, "grub-boot.img"))
	if err != nil {
		return fmt.Errorf("install: this release carries no grub-boot.img, so it cannot boot a BIOS machine: %w", err)
	}
	coreImg, err := os.ReadFile(filepath.Join(slotMount, "grub-core.img"))
	if err != nil {
		return fmt.Errorf("install: this release carries no grub-core.img, so it cannot boot a BIOS machine: %w", err)
	}
	plan, err := planGRUBBootSectors(bootImg, coreImg, biosBoot)
	if err != nil {
		return fmt.Errorf("install: %w", err)
	}
	disk, err := os.OpenFile(diskDev, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	writeErr := plan.write(disk)
	if err := disk.Close(); writeErr == nil {
		writeErr = err
	}
	if writeErr != nil {
		return fmt.Errorf("install: writing the boot sectors on %s: %w", diskDev, writeErr)
	}

	// The boot home: the rendered config and a fresh environment
	// block that name slot A as the default. Slot A is where this
	// install just put the release.
	home := roleMounts[machine.BootHomeRole].path
	grubDir := filepath.Join(home, "grub")
	if err := os.MkdirAll(grubDir, 0o755); err != nil {
		return fmt.Errorf("install: %w", err)
	}
	cfg := renderGRUBConfig(machineName, consoleArgs())
	if err := writeFileDurably(filepath.Join(grubDir, "grub.cfg"), []byte(cfg)); err != nil {
		return fmt.Errorf("install: writing grub.cfg: %w", err)
	}
	env, err := renderGRUBEnv(map[string]string{"default_slot": "A", "try_slot": ""})
	if err != nil {
		return err
	}
	if err := writeFileDurably(filepath.Join(grubDir, "grubenv"), env); err != nil {
		return fmt.Errorf("install: writing grubenv: %w", err)
	}

	fmt.Printf("liken: install: GRUB installed on %s; grub.cfg and grubenv on the boot home prefer slot A\n", diskDev)
	return nil
}

// diskDevice names the whole-disk device that a role's partition
// lives on. Boot-sector writes address the disk, not the partition,
// because the MBR belongs to no partition at all.
func diskDevice(parts []partition, role machine.StorageRoleName) (string, error) {
	name := machine.DeclaredRole{Name: role}.PartitionName()
	for _, p := range parts {
		if p.partName == name {
			return devRoot + "/" + p.disk, nil
		}
	}
	return "", fmt.Errorf("no partition carries the name %q, so its disk cannot be found", name)
}

// hasPartition reports whether a role's partition exists on this
// machine. For the installer, this is the sign that the machine's
// spec declared the role, since storage reconciliation claimed the
// partitions moments before the install began.
func hasPartition(parts []partition, role machine.StorageRoleName) bool {
	name := machine.DeclaredRole{Name: role}.PartitionName()
	for _, p := range parts {
		if p.partName == name {
			return true
		}
	}
	return false
}

// installLayer copies the deployment layer and its sidecar from the
// payload to the slot. The sidecar is the layer's trust root. The
// release document cannot name the layer, because the document is
// public and the layer belongs to one deployment alone. Media that
// carries a layer without its sidecar, or a layer that its sidecar
// rejects, is incomplete, and the install refuses rather than install
// something that no later boot could verify.
func installLayer(slotMount string) error {
	sidecar, err := os.ReadFile(filepath.Join(releasePayloadDir, machine.LayerSidecarName))
	if err != nil {
		return fmt.Errorf("install: reading the layer's sidecar: %w", err)
	}
	digest, err := machine.ParseLayerSidecar(sidecar)
	if err != nil {
		return fmt.Errorf("install: %w", err)
	}

	verify := func(path string) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		return machine.VerifyLayer(digest, f)
	}

	source := filepath.Join(releasePayloadDir, machine.LayerName)
	if err := verify(source); err != nil {
		return fmt.Errorf("install: the layer this image carries doesn't match its sidecar: %w", err)
	}
	dest := filepath.Join(slotMount, machine.LayerName)
	if err := copyDurably(source, dest); err != nil {
		return fmt.Errorf("install: copying %s: %w", machine.LayerName, err)
	}
	if err := verify(dest); err != nil {
		return fmt.Errorf("install: the layer on the slot doesn't verify: %w", err)
	}
	if err := copyDurably(filepath.Join(releasePayloadDir, machine.LayerSidecarName),
		filepath.Join(slotMount, machine.LayerSidecarName)); err != nil {
		return fmt.Errorf("install: copying %s: %w", machine.LayerSidecarName, err)
	}
	fmt.Printf("liken: install: %s verified and installed against its sidecar\n", machine.LayerName)
	return nil
}

// findSlotPartition locates one slot by the name written on it at
// claim time, and reads its GPT identity: the unique GUID that a
// boot entry uses to pin the partition regardless of device
// position.
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
		table, err := disks.ReadGPT(f, disk.SizeBytes/disks.SectorSize)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("install: reading %s's partition table: %w", device, err)
		}
		for _, entry := range table.Entries {
			if entry.Name == declared.PartitionName() {
				number, err := partitionNumber(p)
				if err != nil {
					return nil, err
				}
				return &slotPartition{
					number:   number,
					firstLBA: entry.FirstLBA,
					lastLBA:  entry.LastLBA,
					guid:     entry.UniqueGUID,
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
// node name: the suffix after the disk's name (vdc1 means 1), with
// the "p" separator that NVMe-style names insert (nvme0n1p2 means 2).
// The kernel numbers partitions by their position in the GPT entry
// array, starting at 1. This function refuses a suffix that is not a
// number. The index goes into a firmware boot entry, and 0 is not a
// valid GPT slot, so a malformed name must stop the install rather
// than encode garbage that the firmware would trust.
func partitionNumber(p partition) (uint32, error) {
	suffix := strings.TrimPrefix(p.name, p.disk)
	suffix = strings.TrimPrefix(suffix, "p")
	n, err := strconv.Atoi(suffix)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("install: cannot read a partition number from %q on disk %s", p.name, p.disk)
	}
	return uint32(n), nil
}

// writeSlotBootEntry writes one slot's firmware entry: it boots
// \vmlinuz from this partition, with a command line assembled from
// scratch so that every argument is deliberate and none is inherited
// by accident:
//
//	console=...      copied from this boot, so the installed system
//	                 keeps using whatever console its operator wired
//	rdinit=/liken    run our program as PID 1
//	initrd=          appears three times: \microcode.cpio first (the
//	                 CPU microcode early cpio, which must lead
//	                 because the kernel scans the very start of its
//	                 initrd for microcode before it decompresses
//	                 anything), then \boot.cpio (init and the early
//	                 boot's modules), then \deployment.cpio (this
//	                 deployment's layer), all next to the kernel. The
//	                 EFI stub loads every initrd= file, in order, from
//	                 the same filesystem that it loaded the kernel
//	                 from. The system itself (liken.sqfs) is
//	                 deliberately not among them: init mounts it
//	                 straight from this slot, so the loader stages
//	                 megabytes instead of the whole OS. Composition at
//	                 load time is what lets an upgrade replace the
//	                 generic half without ever touching the layer
//	liken.machine=   the machine's identity, carried from the
//	                 installer boot into this entry
//	liken.slot=      which slot this entry boots, so a running system
//	                 knows which half of the blue-green pair it is on
//	panic=10         reboot ten seconds after a kernel panic, instead
//	                 of hanging forever. Upgrades depend on this: a
//	                 panicking trial slot must reset, so the
//	                 firmware's consumed BootNext can fall back to the
//	                 proven slot
func writeSlotBootEntry(dir, description, slot string, part *slotPartition, machineName string) (uint16, error) {
	args := consoleArgs()
	args = append(args,
		"rdinit=/liken",
		`initrd=\microcode.cpio`,
		`initrd=\boot.cpio`,
		`initrd=\`+machine.LayerName,
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
// command line. The installer was told where this machine's console
// is, and the installed system should keep using the same console.
func consoleArgs() []string {
	var consoles []string
	for _, field := range cmdlineFields() {
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

// copyDurably copies through a temporary name, runs fsync, and
// renames the file, so the slot never holds a file that looks final
// but is not. FAT has no journal, so durability here depends entirely
// on this discipline. Without the explicit sync before the rename,
// the page cache may still hold the file's bytes when the rename
// happens, and a power cut then leaves a final-looking file with
// incomplete contents.
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
