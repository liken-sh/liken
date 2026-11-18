package main

// init lists the machine's disks from /sys/block itself, because a
// liken machine does not run udev.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/liken-sh/liken/liken/machine"
)

// sysBlock and devRoot are variables so that tests can point discovery
// at a fake machine in a temporary directory.
var (
	sysBlock = "/sys/block"
	devRoot  = "/dev"
)

// devicePath returns the /dev node of a disk, which has the same name
// as its /sys/block entry.
func devicePath(d machine.BlockDevice) string {
	return devRoot + "/" + d.Name
}

// discoverBlockDevices returns the machine's disks from /sys/block,
// leaving out virtual devices and the extra areas of an eMMC card.
func discoverBlockDevices() []machine.BlockDevice {
	entries, err := os.ReadDir(sysBlock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: reading %s: %v\n", sysBlock, err)
		return nil
	}

	var disks []machine.BlockDevice
	for _, entry := range entries {
		name := entry.Name()
		dir := filepath.Join(sysBlock, name)

		// /sys/block also lists virtual devices such as loop, ram, and
		// zram. Real storage has a `device` symlink to its bus device
		// (PCI, virtio, USB), and virtual devices have none.
		if _, err := os.Stat(filepath.Join(dir, "device")); err != nil {
			continue
		}

		if mmcHardwareArea(dir) {
			continue
		}

		d := machine.BlockDevice{Name: name}

		// The size file counts 512-byte sectors, whatever the disk's
		// real sector size is.
		if raw, err := os.ReadFile(filepath.Join(dir, "size")); err == nil {
			if sectors, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64); err == nil {
				d.SizeBytes = sectors * 512
			}
		}

		// Which attributes exist depends on the bus. NVMe and SCSI
		// disks have a model and often a serial on the bus device.
		// virtio-blk has only a serial, on the disk itself. A missing
		// attribute is normal.
		d.Model = sysfsString(dir, "device/model")
		d.Serial = sysfsString(dir, "serial", "device/serial")
		if d.Serial == "" {
			// Some SCSI drivers, and libata for a SATA disk, expose no
			// serial attribute. The serial is also in SCSI vital
			// product data page 0x80, which the kernel exposes as
			// device/vpd_pg80 when the device supports the page.
			d.Serial = scsiVPD80Serial(dir)
		}

		d.StableNames = stableNames(name)

		disks = append(disks, d)
	}
	return disks
}

// mmcHardwareArea reports whether a block device is a boot area, a
// general-purpose area, or the RPMB area of an eMMC card. Only the
// card's data area may hold a role. The board's firmware reads the
// boot areas before Linux runs, RPMB is an authenticated store, and
// the general-purpose areas are fixed partitions of the card.
//
// The mmc block driver makes each of these areas a child of the data
// area's block device, so the area's `device` link leads to a block
// device. A disk's `device` link leads to its bus device (PCI, virtio,
// USB, or the mmc card). Kernels before 4.15 made RPMB a block device
// in the same way, and later kernels make it a character device,
// which is not in /sys/block.
func mmcHardwareArea(dir string) bool {
	subsystem, err := filepath.EvalSymlinks(filepath.Join(dir, "device", "subsystem"))
	if err != nil {
		return false
	}
	return filepath.Base(subsystem) == "block"
}

// stableDiskByID and stableDiskByPath are the directories where udev
// makes its disk links, and disklinks.go makes the same links there.
// stableNames computes the names from sysfs as disklinks.go does, so
// a name in the Machine status is a path that a spec can use, even
// before the link exists.
const (
	stableDiskByID   = "/dev/disk/by-id/"
	stableDiskByPath = "/dev/disk/by-path/"
)

// stableNames returns the paths that identify a disk across boots.
// The by-id names come first, because they identify the disk itself.
// The by-path name identifies the port that the disk is connected to.
func stableNames(name string) []string {
	var names []string
	for _, id := range diskIDNames(name) {
		names = append(names, stableDiskByID+id)
	}
	if path := diskPathName(name); path != "" {
		names = append(names, stableDiskByPath+path)
	}
	return names
}

// sysfsString returns the first of the named attributes that exists,
// trimmed of whitespace and NUL bytes. SCSI pads a model with spaces,
// and a serial from a fixed-width buffer can be padded with NULs.
// strings.TrimSpace keeps NUL bytes, and a path with a NUL cannot be
// passed to the kernel, so a disk whose name had one would get no link.
func sysfsString(dir string, names ...string) string {
	for _, name := range names {
		if raw, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			return strings.Trim(string(raw), "\x00 \t\n\r\v\f")
		}
	}
	return ""
}

// reportBlockDevices prints each attached disk for the world report.
func reportBlockDevices() {
	disks := discoverBlockDevices()
	if len(disks) == 0 {
		fmt.Println("liken: no disks attached")
		return
	}
	for _, d := range disks {
		details := []string{gib(d.SizeBytes)}
		if d.Model != "" {
			details = append(details, d.Model)
		}
		if d.Serial != "" {
			details = append(details, "serial "+d.Serial)
		}
		if len(d.StableNames) > 0 {
			details = append(details, d.StableNames[0])
		}
		fmt.Printf("liken: disk %s: %s\n", devicePath(d), strings.Join(details, ", "))
	}
}

// gib formats a byte count in GiB (2^30 bytes). A disk sold as 500 GB
// (decimal) shows as 465.7 GiB.
func gib(b uint64) string {
	return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
}
