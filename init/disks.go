package main

// The machine's disks, discovered by asking the kernel directly.
//
// A full distribution answers the question "what disks does this
// machine have?" with udev: a daemon that receives device events,
// runs a rules engine, and publishes its conclusions as a tree of
// symlinks under /dev/disk. liken does not need the daemon. udev
// gets all of its information by reading sysfs, the kernel's live
// object model, already mounted at /sys. liken is the only program
// here that needs the answer, so it reads the same files itself.
// Each sysfs attribute is a small text file that holds one value, so
// discovery is only directory walks and file reads.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/liken-sh/liken/machine"
)

// These are the roots discovery reads from. They are variables rather
// than constants so that tests can set up a fake machine in a
// tempdir. On a real boot, they never hold anything else.
var (
	sysBlock = "/sys/block"
	devRoot  = "/dev"
)

// devicePath is the node devtmpfs maintains for a disk. The name
// under /dev is the same name sysfs lists; the kernel assigns it in
// both places.
func devicePath(d machine.BlockDevice) string {
	return devRoot + "/" + d.Name
}

// discoverBlockDevices lists the machine's disks: one entry per
// directory in /sys/block that represents real storage. The result
// uses the status type directly, because this same inventory appears
// both in the world report and in the facts init publishes.
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

		// /sys/block lists every block device, including purely
		// virtual ones that the kernel can create without hardware,
		// for example loop, ram, and zram devices. The `device`
		// symlink marks real storage: it points back at the bus
		// device (PCI, virtio, USB) that provides the disk, and
		// virtual devices have no such parent.
		if _, err := os.Stat(filepath.Join(dir, "device")); err != nil {
			continue
		}

		d := machine.BlockDevice{Name: name}

		// The size file counts sectors of 512 bytes. It always uses
		// 512, whatever the disk's real sector size is, because the
		// kernel's ABI fixed that unit decades ago.
		if raw, err := os.ReadFile(filepath.Join(dir, "size")); err == nil {
			if sectors, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64); err == nil {
				d.SizeBytes = sectors * 512
			}
		}

		// Which identifying attributes exist depends on the bus. NVMe
		// and SCSI disks publish a model, and often a serial, on the
		// bus device. virtio-blk publishes only a serial, directly on
		// the disk. The code reads whatever this disk offers; absence
		// is normal.
		d.Model = sysfsString(dir, "device/model")
		d.Serial = sysfsString(dir, "serial", "device/serial")

		disks = append(disks, d)
	}
	return disks
}

// sysfsString reads the first of the named attributes that exists,
// as a trimmed string. The trimming matters: sysfs values end in a
// newline, and SCSI model strings carry padding spaces out to their
// on-wire field width. Both are artifacts of the transport, not part
// of the value.
func sysfsString(dir string, names ...string) string {
	for _, name := range names {
		if raw, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			return strings.TrimSpace(string(raw))
		}
	}
	return ""
}

// reportBlockDevices prints the world report's storage section: every
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

// gib renders a byte count in binary gigabytes (GiB = 2^30). This is
// why a "20G" drive from QEMU reads as exactly 20.0. Retail drives
// are labeled in decimal gigabytes instead, so they read smaller
// than the number on the sticker.
func gib(b uint64) string {
	return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
}
