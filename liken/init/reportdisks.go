package main

// This file chooses the disks that the report boot considers for its
// proposed manifest. For each block device, the report reads sysfs to
// learn whether it is the install stick, whether it can hold a storage
// role at all (the kernel also lists optical drives and empty card
// readers), and whether it exists only because this boot loaded a
// driver that an install would not have.

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/liken/machine"
)

// installStick is the result of one lookup of the install stick,
// which the report passes to every step that must leave the stick
// alone. Separate lookups could disagree, and a stick that one of them
// missed would be proposed as a data disk, which the "wipe and
// reinstall" entry would then erase.
type installStick struct {
	// Disk is the kernel name of the stick's disk, and Path is its
	// device node. Both are empty unless exactly one partition is
	// named liken:install.
	Disk string
	Path string
	// Partition is the device node of the FAT volume that the report
	// mounts to write the proposal.
	Partition string
	// Candidates lists every disk that has a partition named
	// liken:install.
	Candidates []string
}

// ambiguous reports whether liken:install partitions exist but the
// report could not pick one. The report then writes no proposal to a
// stick, and places no role on any candidate disk.
func (s installStick) ambiguous() bool {
	return s.Disk == "" && len(s.Candidates) > 0
}

// resolveInstallStick finds the stick by the GPT partition name that
// image/stick.go writes, because a device path changes with the order
// in which the kernel enumerates disks.
func resolveInstallStick() installStick {
	var stick installStick
	var partitions []string
	for _, p := range discoverPartitions() {
		if p.partName != stickInstallPartition {
			continue
		}
		partitions = append(partitions, p.name)
		if !slices.Contains(stick.Candidates, p.disk) {
			stick.Candidates = append(stick.Candidates, p.disk)
		}
	}
	if len(partitions) != 1 {
		return stick
	}
	stick.Disk = stick.Candidates[0]
	stick.Path = devRoot + "/" + stick.Disk
	stick.Partition = devRoot + "/" + partitions[0]
	return stick
}

// awaitInstallStick polls every 200ms until a liken:install partition
// appears or stickCeiling passes, and returns the last lookup. It
// polls the partitions instead of waiting for a uevent, so a disk
// whose partitions appear after the disk itself is still found.
//
// The wait is needed because usb-storage waits one second after a
// device attaches before it scans it (its delay_use parameter), and
// USB enumeration can continue after the settle that the report does
// before this. A boot with no stick, such as one started by typing
// liken.report by hand, waits the full stickCeiling and then shows its
// report on the console.
func awaitInstallStick() installStick {
	deadline := time.Now().Add(stickCeiling)
	for {
		stick := resolveInstallStick()
		if len(stick.Candidates) > 0 || time.Now().After(deadline) {
			return stick
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// readReportDisks returns the disks that could hold a storage role,
// and separately the memory cards in slots. It leaves out the
// install stick. When the stick is ambiguous, each candidate
// stays in the list with MaybeStick set, so the proposal shows it but
// places no role on it.
func readReportDisks(stick installStick) (disks, cards []reportDisk) {
	for _, d := range discoverBlockDevices() {
		if d.Name == stick.Disk || !canHoldARole(d) {
			continue
		}
		measured := reportDisk{
			Name:       d.Name,
			Path:       devicePath(d),
			SizeBytes:  d.SizeBytes,
			Model:      d.Model,
			Transport:  diskTransport(d.Name),
			StableName: firstByIDName(d.StableNames),
			MaybeStick: stick.ambiguous() && slices.Contains(stick.Candidates, d.Name),
		}
		if cardInASlot(d.Name) {
			cards = append(cards, measured)
			continue
		}
		disks = append(disks, measured)
	}
	return disks, cards
}

// cardInASlot reports whether a block device is a removable memory
// card, as opposed to an eMMC module soldered to the board. A card can
// be taken out like the install stick, so the report lists it but
// proposes no role on it. A spec that a person writes can still name
// an SD card, and the install uses it. The mmc bus sets device/type to
// MMC for an eMMC module, SD for an SD card, and SDcombo for an SD
// card with SDIO.
func cardInASlot(name string) bool {
	switch sysfsString(filepath.Join(sysBlock, name), "device/type") {
	case "SD", "SDcombo":
		return true
	}
	return false
}

// firstByIDName returns the first by-id name among a disk's stable
// names, or "". A by-path name is skipped because it identifies the
// port the disk is on, not the disk.
func firstByIDName(names []string) string {
	for _, name := range names {
		if strings.HasPrefix(name, stableDiskByID) {
			return name
		}
	}
	return ""
}

// canHoldARole reports whether a storage role can be placed on a block
// device. It rejects only a device with no medium, a read-only device,
// and an optical drive, because none of them can take a partition
// table. The checks are narrow because rejecting a usable disk is
// worse than proposing an unusable one. There is no minimum size, for
// example, because some industrial boards boot from a SATA DOM of a
// few gigabytes.
func canHoldARole(d machine.BlockDevice) bool {
	if d.SizeBytes == 0 {
		return false
	}
	dir := filepath.Join(sysBlock, d.Name)
	if sysfsString(dir, "ro") == "1" {
		return false
	}
	// device/type is the SCSI peripheral device type: 5 is a CD or DVD
	// drive and 7 is optical memory. SATA and USB disks have it too,
	// because libata and usb-storage present them as SCSI devices.
	switch sysfsString(dir, "device/type") {
	case "5", "7":
		return false
	}
	return true
}

// diskTransport returns the bus that a disk attaches through, or "",
// so that a person can recognize the disk in the proposal. The disk's
// resolved sysfs path has a directory for each controller between the
// disk and the CPU, such as ata2 for a SATA disk.
func diskTransport(name string) string {
	real, err := filepath.EvalSymlinks(filepath.Join(sysBlock, name))
	if err != nil {
		return ""
	}
	// The first match wins. A path can contain more than one marker,
	// and this order picks the bus that the person would name.
	for _, bus := range []struct{ marker, transport string }{
		{"/nvme", "nvme"},
		{"/ata", "sata"},
		{"/usb", "usb"},
		{"/virtio", "virtio"},
		// eMMC modules and SD cards are under an mmc_host directory.
		{"/mmc", "mmc"},
	} {
		if strings.Contains(real, bus.marker) {
			return bus.transport
		}
	}
	return ""
}

// markDisksBehindDrivers sets BehindModules on each disk that is below
// a device of a module recommendation, to the modules of those
// recommendations' Chain. The image's boot modules had no driver for
// that device, and an install mounts the manifest's disks before it
// loads spec.modules. So the machine needs an image with the driver in
// its boot modules, and the proposal says so.
func markDisksBehindDrivers(disks []reportDisk, recs []moduleRecommendation) []reportDisk {
	for i, d := range disks {
		for _, rec := range recs {
			for _, dir := range rec.SysfsDirs {
				if blockDeviceUnder(d.Name, dir) {
					disks[i].BehindModules = appendNew(d.BehindModules, rec.Chain...)
					break
				}
			}
		}
	}
	return disks
}

// blockDeviceUnder reports whether a disk is below a device in the
// sysfs device tree. It resolves both paths first, because the entries
// in /sys/block are symlinks into /sys/devices.
func blockDeviceUnder(diskName, deviceDir string) bool {
	if diskName == "" {
		return false
	}
	disk, err := filepath.EvalSymlinks(filepath.Join(sysBlock, diskName))
	if err != nil {
		return false
	}
	device, err := filepath.EvalSymlinks(deviceDir)
	if err != nil {
		return false
	}
	return strings.HasPrefix(disk+"/", device+"/")
}

// withoutStickRecommendations drops each module recommendation whose
// devices all have the stick's disk below them. The report loaded that
// driver only to write the proposal to the stick, and the stick does
// not stay in the machine. A recommendation that also has another
// device, such as a permanent disk on the same kind of controller,
// stays.
func withoutStickRecommendations(recs []moduleRecommendation, stick string) []moduleRecommendation {
	if stick == "" {
		return recs
	}
	var kept []moduleRecommendation
	for _, rec := range recs {
		serves := len(rec.SysfsDirs) > 0
		for _, dir := range rec.SysfsDirs {
			if !blockDeviceUnder(stick, dir) {
				serves = false
				break
			}
		}
		if !serves {
			kept = append(kept, rec)
		}
	}
	return kept
}

// appendNew appends each name that the list does not contain yet, in
// the order given.
func appendNew(existing []string, names ...string) []string {
	for _, name := range names {
		if !slices.Contains(existing, name) {
			existing = append(existing, name)
		}
	}
	return existing
}
