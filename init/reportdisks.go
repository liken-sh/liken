package main

// The report boot's view of the machine's block devices.
//
// Before the report can propose a manifest it has to answer three
// questions about the disks in front of it. Which device is the
// installation stick, so that no role lands on the one disk that
// leaves with the person? Which of the rest can hold a role at all,
// as against an optical drive or an empty card reader that the kernel
// also presents as a block device? And which of those can an install
// reach, as against a disk that exists only because this boot loaded
// a driver? Every answer is a small read of sysfs, the way the rest
// of liken discovers storage: the kernel publishes these facts
// already, so nothing here has to derive them.

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/machine"
)

// installStick is the report's one answer about the installation
// stick, resolved once and threaded through everything that needs it:
// the disk walk that leaves the stick out, the proposal that places no
// role on it, and the write of the proposal onto it. Two independent
// lookups could disagree, and a disagreement here has teeth. A stick
// that one lookup missed would be proposed as a data disk, and the
// "wipe and reinstall" entry blanks every disk the manifest names.
type installStick struct {
	// Disk is the kernel name of the disk that carries the stick, and
	// Path is that disk's device node. Both are empty when the report
	// cannot name one disk with confidence.
	Disk string
	Path string
	// Partition is the device node of the FAT volume to mount to write
	// the proposal.
	Partition string
	// Candidates names every disk that carries a partition called
	// liken:install. One is the ordinary case. More than one is an
	// ambiguity the report refuses to guess through, and every disk in
	// the list must then stay out of the proposal.
	Candidates []string
}

// ambiguous reports whether more than one disk claims to be the
// stick. The report can neither write to such a stick nor place a
// role on any disk that might be it.
func (s installStick) ambiguous() bool {
	return s.Disk == "" && len(s.Candidates) > 0
}

// resolveInstallStick finds the installation stick the same way liken
// recognizes everything else on a disk: by the GPT partition name that
// image/stick.go writes, not by a device path, which changes with
// enumeration order.
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

// awaitInstallStick polls for the installation stick until it appears
// or the ceiling passes, ending the moment anything claims to be one.
// The poll is on the partition table itself, not on uevents, so it
// also covers the moment between the disk's arrival and its
// partitions'.
//
// The wait exists because usb-storage delays its scan a full second
// after the device attaches (its delay_use), and USB enumeration runs
// on past the settle the report does before this. An immediate walk
// can therefore run before the stick's disk exists. A report boot
// expects a stick by construction: a person picked the entry from one.
// A boot with no stick at all (a hand-typed liken.report) pays the
// ceiling once, and its report stays on the console.
func awaitInstallStick(ceiling time.Duration) installStick {
	deadline := time.Now().Add(ceiling)
	for {
		stick := resolveInstallStick()
		if len(stick.Candidates) > 0 || time.Now().After(deadline) {
			return stick
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// readReportDisks lists the disks that could carry a storage role, in
// the form the proposal records. It reuses the same sysfs walk the
// world report uses, and adds the transport, which a person reads to
// recognize a disk by how it attaches. The installation stick is left
// out entirely, because it belongs to the person and not to the
// machine. A disk that only might be the stick stays in the list as
// evidence, marked, so the proposal can account for it and still place
// no role on it.
func readReportDisks(stick installStick) []reportDisk {
	var disks []reportDisk
	for _, d := range discoverBlockDevices() {
		if d.Name == stick.Disk || !canHoldARole(d) {
			continue
		}
		disks = append(disks, reportDisk{
			Name:       d.Name,
			Path:       devicePath(d),
			SizeBytes:  d.SizeBytes,
			Model:      d.Model,
			Transport:  diskTransport(d.Name),
			MaybeStick: stick.ambiguous() && slices.Contains(stick.Candidates, d.Name),
		})
	}
	return disks
}

// canHoldARole rejects the block devices that a storage role cannot
// live on. /sys/block lists every device with a bus parent, and a
// repurposed desktop offers several that no partition table belongs
// on: an optical drive, a card reader with no card in it, a
// write-protected medium. A role proposed onto one of those is a
// layout that cannot be claimed.
//
// The three checks are deliberately narrow, because the mistake in the
// other direction is worse. A small disk is still a disk: a SATA DOM
// of a few gigabytes is exactly the boot device some industrial boards
// ship with, and a memory card holding a role is a legitimate, if
// slow, machine. So the filter tests only for the absence of a medium,
// a read-only device, and the SCSI device types that name optical
// drives.
func canHoldARole(d machine.BlockDevice) bool {
	if d.SizeBytes == 0 {
		return false
	}
	dir := filepath.Join(sysBlock, d.Name)
	if sysfsString(dir, "ro") == "1" {
		return false
	}
	// The SCSI peripheral device type is the kernel's own word for
	// what a device is. Type 5 is a CD or DVD drive and type 7 is
	// optical memory; an ordinary disk is type 0. Every disk that
	// arrives through the SCSI layer publishes this attribute, which
	// includes SATA and USB disks, because libata and usb-storage both
	// present their devices to that layer.
	switch sysfsString(dir, "device/type") {
	case "5", "7":
		return false
	}
	return true
}

// diskTransport names the bus that carries a disk, read from the disk's
// place in the sysfs device tree. The tree's path names every bus the
// device hangs off, so a SATA disk's path passes through an ata node, an
// NVMe disk's through nvme, and so on. This is the same information udev
// derives its ID_BUS from, read straight from the one file the kernel
// already maintains.
func diskTransport(name string) string {
	real, err := filepath.EvalSymlinks(filepath.Join(sysBlock, name))
	if err != nil {
		return ""
	}
	// The order matters only where a path could match more than one
	// word. A disk reached through libata shows both an ata node and the
	// SCSI layer that libata presents it through, and "sata" is the
	// answer a person wants.
	for _, bus := range []struct{ marker, transport string }{
		{"/nvme", "nvme"},
		{"/ata", "sata"},
		{"/usb", "usb"},
		{"/virtio", "virtio"},
	} {
		if strings.Contains(real, bus.marker) {
			return bus.transport
		}
	}
	return ""
}

// markDisksBehindDrivers finds the disks that exist only because this
// report loaded a driver, and records which chain reached them.
//
// A device is unclaimed exactly because the boot path had no driver
// for it. So a disk that sits below such a device is a disk the boot
// path cannot see, and the install path is the boot path: it claims
// and mounts the manifest's disks before it loads a single module the
// manifest declares. Naming the driver in spec.modules would not
// help, and would read as though it did. The proposal says so instead,
// and the machine needs an image that carries the driver in its boot
// modules.
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

// blockDeviceUnder reports whether a disk sits below one device in the
// sysfs device tree. Both paths resolve through their symlinks first,
// because /sys/block holds links into /sys/devices, where the real
// parent-and-child structure is.
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

// withoutStickRecommendations drops the recommendations that exist
// only to reach the installation stick. The stick's own controller
// wants a driver like any unclaimed device, and the report rightly
// loaded it, because without it there is no stick to write the
// proposal to. But the stick leaves the machine with the person, so
// its driver does not belong in the machine's manifest. A device
// serves the stick when the stick's disk sits below it in the sysfs
// device tree; a recommendation is dropped only when every device
// that named it serves the stick, so a second, permanent disk on the
// same kind of controller keeps its driver recommended.
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

// appendNew adds the names that are not in the list already, keeping
// the order they arrive in. Two controllers of the same kind reach
// their disks through the same chain, and a disk names each chain
// that reached it once.
func appendNew(existing []string, names ...string) []string {
	for _, name := range names {
		if !slices.Contains(existing, name) {
			existing = append(existing, name)
		}
	}
	return existing
}
