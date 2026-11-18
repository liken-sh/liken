package main

// The installer copies the release payload from external media onto the
// machine's own disk.
//
// A machine boots in install mode when liken.install is on its kernel
// command line, booted from QEMU's -kernel flag in the lab, or from an
// install stick or PXE on real hardware. The installer verifies the
// release payload in the image, copies it into system slot A, registers
// both slots with the boot loader (firmware boot entries on a UEFI
// machine, liken's GRUB on a machine that declares the biosBoot and
// bootHome roles), and powers off. Every later boot comes from the
// disk.
//
// The installer runs inside the normal init, after the same storage
// reconciliation, which claims and formats the slots on a fresh
// machine, and the same manifest selection. It powers off instead of
// rebooting, because a reboot would start the installer again from the
// install medium.
//
// When an install crashes, running it again gives the same result.
// Storage reconciliation finds the slots it already claimed by their
// partition names, a half-written file fails verification and is copied
// again, and boot entries are found by description and rewritten in
// place.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/disks"
	"github.com/liken-sh/liken/liken/machine"
)

// releasePayloadDir is the directory of the release that the install
// image contains: the artifacts that the release document lists, plus
// the deployment layer and its sidecar. image/media.go appends it to
// the initramfs as one more cpio archive, and the kernel unpacks
// concatenated archives in order. It is a variable so that tests can
// supply their own payload.
var releasePayloadDir = "/usr/share/liken/release"

// installSlot is the slot that an install writes. Slot B stays empty
// until the first upgrade fills it.
const installSlot = "A"

// installToDisk runs the whole install against the slots that storage
// reconciliation just mounted, and returns the letter of the slot that
// it wrote. It does not power off, so that main can apply the boot
// policy first.
func installToDisk(machineName string) (string, error) {
	if machineName == "" {
		return "", fmt.Errorf("install: this machine has no name. Boot entries identify the machine, so set liken.machine= or declare the name in a manifest")
	}

	// Both slots must exist before anything is copied, because the
	// fallback slot has to be registered from the first boot.
	parts := discoverPartitions()
	slotA, err := findSlotPartition(parts, machine.SystemARole)
	if err != nil {
		return "", err
	}
	slotB, err := findSlotPartition(parts, machine.SystemBRole)
	if err != nil {
		return "", err
	}

	raw, err := os.ReadFile(filepath.Join(releasePayloadDir, "release.yaml"))
	if err != nil {
		return "", fmt.Errorf("install: reading the release document: %w", err)
	}
	release, err := machine.ParseRelease(raw)
	if err != nil {
		return "", fmt.Errorf("install: %w", err)
	}
	fmt.Printf("liken: install: installing release %s, which lists %d artifacts\n", release.Metadata.Name, len(release.Artifacts))

	slotMount := roleMounts[machine.SystemARole].path
	for _, artifact := range release.Artifacts {
		source := filepath.Join(releasePayloadDir, artifact.Name)
		if err := verifyFile(artifact, source); err != nil {
			return "", fmt.Errorf("install: %s in this image does not match the release document: %w", artifact.Name, err)
		}
		dest := filepath.Join(slotMount, artifact.Name)
		if err := copyDurably(source, dest); err != nil {
			return "", fmt.Errorf("install: copying %s: %w", artifact.Name, err)
		}
		// Hash the copy on the slot too, so that a torn or corrupted
		// write fails the install now and not a later boot.
		if err := verifyFile(artifact, dest); err != nil {
			return "", fmt.Errorf("install: %s on slot %s does not match the release document: %w", artifact.Name, installSlot, err)
		}
		fmt.Printf("liken: install: verified and installed %s (%d bytes)\n", artifact.Name, artifact.Size)
	}

	// The deployment layer is not an artifact of the release document,
	// because that document is public and the layer belongs to one
	// deployment. Its sidecar holds its digest instead. installLayer
	// writes the sidecar last, so a slot that has a sidecar has a
	// complete layer.
	if err := installLayer(slotMount); err != nil {
		return "", err
	}

	// The release document goes on the slot after the artifacts it
	// lists, so a slot that has release.yaml has every artifact.
	// verifySlotContents (fatstate.go) reads it to check the slot on a
	// later boot. machine-operator writes the same file last when it
	// downloads a release into the other slot.
	if err := copyDurably(filepath.Join(releasePayloadDir, "release.yaml"),
		filepath.Join(slotMount, "release.yaml")); err != nil {
		return "", fmt.Errorf("install: copying the release document to slot %s: %w", installSlot, err)
	}

	// A UEFI machine gets firmware boot entries, and a machine whose
	// spec declares the GRUB roles gets liken's GRUB. A machine can get
	// both, so that one disk boots under UEFI or BIOS. An install that
	// no firmware could boot fails here, before the power-off.
	actuators := 0
	if firmwareIsUEFI() {
		// The slot loader at the firmware's default boot path boots the
		// machine when the firmware has lost its boot entries
		// (slotloader.go).
		if err := writeSlotLoader(slotMount, installSlot, machineName); err != nil {
			return "", fmt.Errorf("install: %w", err)
		}
		if err := registerSlotEntries(slotA, slotB, machineName); err != nil {
			return "", fmt.Errorf("install: %w", err)
		}
		actuators++
	}
	if hasPartition(parts, machine.BIOSBootRole) {
		if err := installGRUB(parts, machineName, slotMount); err != nil {
			return "", err
		}
		actuators++
	}
	if actuators == 0 {
		return "", fmt.Errorf("install: this machine boots by BIOS and its spec declares no biosBoot role, so nothing can boot the installed disk")
	}

	// Flush everything to the disk before the caller prints success. An
	// attended install waits at the console after that message, and a
	// person may cut power instead of pressing Enter. copyDurably makes
	// each file's data durable, but on FAT the rename that gave the
	// file its final name stays in the page cache until something
	// flushes it.
	//
	// unix.Sync writes the dirty pages of every mounted filesystem to
	// the drivers, but does not flush the drive's write cache.
	// syncDirectory does that for the slot.
	unix.Sync()
	if err := syncDirectory(slotMount); err != nil {
		return "", fmt.Errorf("install: flushing the directory of slot %s to the disk: %w", installSlot, err)
	}
	return installSlot, nil
}

// installGRUB writes GRUB for a machine that boots by BIOS: the
// patched boot code into the MBR and the biosBoot partition
// (grubinstall.go computes the sector layout), and grub.cfg and the
// environment block onto the boot home. It reads the GRUB images from
// slot A, which installToDisk has already verified. A second run writes
// the same bytes.
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
		return fmt.Errorf("install: reading grub-boot.img from the slot, which a BIOS machine needs to boot: %w", err)
	}
	coreImg, err := os.ReadFile(filepath.Join(slotMount, "grub-core.img"))
	if err != nil {
		return fmt.Errorf("install: reading grub-core.img from the slot, which a BIOS machine needs to boot: %w", err)
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
		return fmt.Errorf("install: writing the GRUB boot sectors to %s: %w", diskDev, writeErr)
	}

	// The config and a fresh environment block make slot A the default,
	// because slot A holds the release that this install wrote.
	home := roleMounts[machine.BootHomeRole].path
	grubDir := filepath.Join(home, "grub")
	if err := os.MkdirAll(grubDir, 0o755); err != nil {
		return fmt.Errorf("install: %w", err)
	}
	cfg := renderGRUBConfig(machineName, consoleArgs())
	if err := writeFileDurably(filepath.Join(grubDir, "grub.cfg"), []byte(cfg)); err != nil {
		return fmt.Errorf("install: writing grub.cfg: %w", err)
	}
	env, err := renderGRUBEnv(map[string]string{"default_slot": installSlot, "try_slot": ""})
	if err != nil {
		return err
	}
	if err := writeFileDurably(filepath.Join(grubDir, "grubenv"), env); err != nil {
		return fmt.Errorf("install: writing grubenv: %w", err)
	}
	// Flush both renames and the drive's write cache, because GRUB
	// stops at its rescue prompt when a power cut loses grub.cfg.
	if err := syncDirectory(grubDir); err != nil {
		return fmt.Errorf("install: flushing the boot home to the disk: %w", err)
	}

	fmt.Printf("liken: install: installed GRUB on %s, with grub.cfg and grubenv on the boot home set to boot slot A\n", diskDev)
	return nil
}

// diskDevice returns the whole-disk device that holds a role's
// partition. The MBR is outside every partition, so the boot-sector
// writes address the disk.
func diskDevice(parts []partition, role machine.StorageRoleName) (string, error) {
	name := machine.DeclaredRole{Name: role}.PartitionName()
	for _, p := range parts {
		if p.partName == name {
			return devRoot + "/" + p.disk, nil
		}
	}
	return "", fmt.Errorf("no partition is named %q, so its disk is unknown", name)
}

// hasPartition reports whether a partition for the role exists. Storage
// reconciliation creates partitions only for the roles that the
// machine's spec declares.
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
// payload to the slot. The sidecar holds the layer's digest. When the
// sidecar is missing, or the layer does not match it, the install fails
// because no later boot could verify the layer.
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
		return fmt.Errorf("install: the layer in this image does not match its sidecar: %w", err)
	}
	dest := filepath.Join(slotMount, machine.LayerName)
	if err := copyDurably(source, dest); err != nil {
		return fmt.Errorf("install: copying %s: %w", machine.LayerName, err)
	}
	if err := verify(dest); err != nil {
		return fmt.Errorf("install: the layer on slot %s does not match its sidecar: %w", installSlot, err)
	}
	if err := copyDurably(filepath.Join(releasePayloadDir, machine.LayerSidecarName),
		filepath.Join(slotMount, machine.LayerSidecarName)); err != nil {
		return fmt.Errorf("install: copying %s: %w", machine.LayerSidecarName, err)
	}
	fmt.Printf("liken: install: verified %s against its sidecar and installed it\n", machine.LayerName)
	return nil
}

// findSlotPartition finds a slot by the partition name that storage
// reconciliation wrote when it claimed the slot, and reads the slot's
// GPT entry. A boot entry uses the entry's unique GUID to identify the
// partition wherever the disk appears.
func findSlotPartition(parts []partition, role machine.StorageRoleName) (*slotPartition, error) {
	declared := machine.DeclaredRole{Name: role}
	for _, p := range parts {
		if p.partName != declared.PartitionName() {
			continue
		}
		device := devRoot + "/" + p.disk
		disk := diskByPath(device)
		if disk == nil {
			return nil, fmt.Errorf("install: the %s partition is on %s, which is not in the disk inventory", role, device)
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
		return nil, fmt.Errorf("install: sysfs lists the %s partition on %s, but the partition table of %s has no entry named %s", role, device, device, declared.PartitionName())
	}
	return nil, fmt.Errorf("install: no partition is named %s. The manifest must declare both system slots before the machine can install itself", declared.PartitionName())
}

type slotPartition struct {
	number   uint32
	firstLBA uint64
	lastLBA  uint64
	guid     [16]byte
}

// partitionNumber reads the partition number from the kernel's device
// name: the suffix after the disk's name (vdc1 is 1), without the "p"
// that NVMe-style names add (nvme0n1p2 is 2). The number goes into a
// firmware boot entry, and the kernel numbers partitions from 1, so a
// suffix that is not a number of at least 1 fails the install.
func partitionNumber(p partition) (uint32, error) {
	suffix := strings.TrimPrefix(p.name, p.disk)
	suffix = strings.TrimPrefix(suffix, "p")
	n, err := strconv.ParseUint(suffix, 10, 32)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("install: the partition name %q on disk %s has no partition number of 1 or more", p.name, p.disk)
	}
	return uint32(n), nil
}
