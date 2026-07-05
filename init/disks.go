package main

// The machine's disks, discovered by asking the kernel directly.
//
// A full distribution answers "what disks does this machine have?"
// with udev: a daemon that fields device events, runs a rules engine,
// and publishes its conclusions as a tree of symlinks under /dev/disk.
// liken doesn't need the daemon. Everything udev knows, it learned by
// reading sysfs (the kernel's live object model, already mounted at
// /sys), and liken is the only program here that wants the answer, so
// it reads the same files itself. Each sysfs attribute is a tiny text
// file holding one value; discovery is just directory walks and reads.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chrisguidry/liken/machine"
)

// The roots discovery reads from. Variables rather than constants so
// tests can stand up a fake machine in a tempdir; on a real boot they
// are never anything else.
var (
	sysBlock = "/sys/block"
	devRoot  = "/dev"
)

// devicePath is the node devtmpfs maintains for a disk; the name
// under /dev is the same name sysfs lists, kernel-assigned in both
// places.
func devicePath(d machine.BlockDevice) string {
	return devRoot + "/" + d.Name
}

// discoverBlockDevices enumerates the machine's disks: one entry per
// directory in /sys/block that stands for real storage. The result is
// the status type directly: this same inventory is a section of the
// world report and a block of the facts init publishes.
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

		// /sys/block lists every block device, including the purely
		// virtual ones the kernel can create without hardware (loop,
		// ram, zram). What marks real storage is the `device` symlink:
		// it points back at the bus device (PCI, virtio, USB) that
		// provides the disk, and virtual devices have no such parent.
		if _, err := os.Stat(filepath.Join(dir, "device")); err != nil {
			continue
		}

		d := machine.BlockDevice{Name: name}

		// The size file counts sectors of 512 bytes: always 512,
		// whatever the disk's real sector size, because that unit
		// was fixed into the kernel's ABI decades ago.
		if raw, err := os.ReadFile(filepath.Join(dir, "size")); err == nil {
			if sectors, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64); err == nil {
				d.SizeBytes = sectors * 512
			}
		}

		// Which identifying attributes exist depends on the bus: NVMe
		// and SCSI disks publish a model (and often a serial) on the
		// bus device, virtio-blk publishes only a serial, directly on
		// the disk. Read whatever this disk offers; absence is normal.
		d.Model = sysfsString(dir, "device/model")
		d.Serial = sysfsString(dir, "serial", "device/serial")

		disks = append(disks, d)
	}
	return disks
}

// sysfsString reads the first of the named attributes that exists,
// as a trimmed string. The trimming matters: sysfs values end in a
// newline, and SCSI model strings are padded with spaces to their
// on-wire field width; transport artifacts, not data.
func sysfsString(dir string, names ...string) string {
	for _, name := range names {
		if raw, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			return strings.TrimSpace(string(raw))
		}
	}
	return ""
}

// reportBlockDevices is the world report's storage section: every
// disk attached to this machine.
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
		fmt.Printf("liken: disk %s: %s\n", devicePath(d), strings.Join(details, ", "))
	}
}

// gib renders a byte count in binary gigabytes (GiB = 2^30), which is
// why a "20G" drive from QEMU reads as exactly 20.0, unlike retail
// drives, which are labeled in decimal gigabytes and read smaller
// than the sticker.
func gib(b uint64) string {
	return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
}
