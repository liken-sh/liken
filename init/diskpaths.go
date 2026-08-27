package main

// Local by-path names: udev's path_id for a disk on this machine's
// own PCI, SATA, USB, or virtio bus.
//
// A by-id name follows the disk. A by-path name follows the port: it
// stays the same when the disk moves, because it names the wire the
// disk sits on rather than the disk's own firmware. This is why a
// port name matters at all, on top of the identity names diskids.go
// already builds. Wiping and swapping in a spare drive is easier when
// an operator can say "whatever sits in the bay wired here" instead
// of tracking a serial number, and a spec that says so keeps working
// after the swap.
//
// udev builds the same name in its path_id builtin, by walking a
// disk's chain of parent devices in sysfs from the disk up to the
// bus. Every step of that chain is a directory name the kernel
// already publishes, in the resolved path behind /sys/block/<name>,
// so this file walks the same chain udev does and reads nothing udev
// would not also read.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// pciSlotPattern matches a PCI address segment: a four-digit domain, a
// two-digit bus, and a two-digit device and function, the way the
// kernel names a PCI device's directory. A disk's path can pass
// through more than one such segment, for example a root port and the
// storage controller behind it, so diskPathName keeps the last match:
// the slot closest to the disk is the one that identifies its port.
var pciSlotPattern = regexp.MustCompile(`^[0-9a-fA-F]{4}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2}\.[0-9a-fA-F]$`)

// ataSegmentPattern matches the ataN directory libata creates for
// each port it drives, one per SATA disk.
var ataSegmentPattern = regexp.MustCompile(`^ata(\d+)$`)

// virtioSegmentPattern matches the virtioN directory the virtio bus
// creates for each device, disk or otherwise, attached to it.
var virtioSegmentPattern = regexp.MustCompile(`^virtio\d+$`)

// usbPortSegmentPattern matches a USB device's own directory, named
// for its position in the bus: the bus number, a dash, and the port
// path down through any hubs to reach it. A USB device's interface
// sits one level below, in a directory named the same way with
// ":<interface>" appended, which this pattern excludes on purpose so
// it never matches the interface by mistake.
var usbPortSegmentPattern = regexp.MustCompile(`^\d+-[0-9.]+$`)

// iscsiSessionPattern matches the sessionN directory scsi_transport_iscsi
// creates for each login. A session number is reassigned on every
// login, so a name built from it would change out from under a mount
// on the next reboot; disklinks.go already gives an iSCSI disk a
// stable by-path name built from its target and address instead.
var iscsiSessionPattern = regexp.MustCompile(`^session\d+$`)

// nvmeNsidPattern reads the namespace ID off the end of an NVMe block
// device's own name, for example the 1 in nvme0n1. diskPathName only
// falls back to this parse when the kernel's own nsid attribute is
// absent, because the two numbers agree only when a controller's
// namespaces are created in order starting at 1: the block name's
// trailing digit is the Nth namespace this controller probed, while
// the nsid attribute is the namespace's own number as the controller
// reports it. A controller that lost namespace 1 and kept only
// namespace 2 still names its sole block device nvme0n1, because the
// kernel numbers by probe order, so a name built from that digit
// would claim namespace 1 while the disk is actually namespace 2.
var nvmeNsidPattern = regexp.MustCompile(`n(\d+)$`)

// mmcHostSegment is the directory the mmc core creates under a host
// controller to hold the cards on it. It sits directly below the
// controller's own device, so the segment before it names the
// controller.
const mmcHostSegment = "mmc_host"

// mmcPathName is udev's by-path answer for an eMMC module or an SD
// card, which is a name only when the host controller is a platform
// device.
//
// udev's path_id builtin has no mmc handler at all. It walks the
// parents of a block device and prepends a segment for each bus it
// recognizes, and mmc is not one of them. A card's parents are the mmc
// card, the mmc host, and then the controller, so the name a card gets
// is the name of its controller and nothing else. A controller the
// firmware enumerates through ACPI or a device tree is a platform
// device, and path_id names a platform device platform-<name>, with
// the PCI slot ahead of it when the platform device sits under a PCI
// function. That is where an eMMC's by-path name comes from.
//
// A controller that enumerates over PCI on its own, which is what
// sdhci-pci drives, gets no name. path_id counts a PCI slot as a
// parent and not as a transport, and it refuses to name a block device
// whose walk found no transport, so udev writes no ID_PATH and builds
// no link under /dev/disk/by-path. liken publishes no name there
// either, because a by-path name that udev does not build is a name no
// other tool on the machine agrees with. Such a card is named by its
// by-id name alone (diskids.go).
func mmcPathName(segments []string, pciSlot string) string {
	host := -1
	for i, seg := range segments {
		if seg == mmcHostSegment {
			host = i - 1
			break
		}
	}
	if host < 0 {
		return ""
	}
	dir := strings.Join(segments[:host+1], string(filepath.Separator))
	subsystem, err := filepath.EvalSymlinks(filepath.Join(dir, "subsystem"))
	if err != nil || filepath.Base(subsystem) != "platform" {
		return ""
	}
	if pciSlot != "" {
		return "pci-" + pciSlot + "-platform-" + segments[host]
	}
	return "platform-" + segments[host]
}

// diskPathName computes the local by-path name for one disk: the port
// it answers on, read from the resolved sysfs path behind
// /sys/block/<name>. It returns "" for a disk with no such name,
// which includes any disk this machine reaches over iSCSI, and any
// disk whose bus this function does not recognize.
func diskPathName(name string) string {
	real, err := filepath.EvalSymlinks(filepath.Join(sysBlock, name))
	if err != nil {
		return ""
	}
	segments := strings.Split(real, string(filepath.Separator))

	for _, seg := range segments {
		if iscsiSessionPattern.MatchString(seg) {
			return ""
		}
	}

	pciSlot := ""
	for _, seg := range segments {
		if pciSlotPattern.MatchString(seg) {
			pciSlot = seg
		}
	}

	if name := mmcPathName(segments, pciSlot); name != "" {
		return name
	}

	if pciSlot == "" {
		return ""
	}

	for _, seg := range segments {
		if m := ataSegmentPattern.FindStringSubmatch(seg); m != nil {
			return "pci-" + pciSlot + "-ata-" + m[1]
		}
	}
	for _, seg := range segments {
		if seg == "nvme" {
			// udev's path_id builtin reads the namespace's own nsid
			// attribute rather than parsing the block name, so this
			// does the same and only parses the name on an old kernel
			// that predates the attribute.
			if nsid := sysfsString(filepath.Join(sysBlock, name), "nsid"); nsid != "" {
				return "pci-" + pciSlot + "-nvme-" + nsid
			}
			if nsid := nvmeNsidPattern.FindStringSubmatch(name); nsid != nil {
				return "pci-" + pciSlot + "-nvme-" + nsid[1]
			}
			return ""
		}
	}
	for _, seg := range segments {
		if !usbPortSegmentPattern.MatchString(seg) {
			continue
		}
		hctl := hctlPattern.FindStringSubmatch(real)
		if hctl == nil {
			return ""
		}
		portpath := strings.SplitN(seg, "-", 2)[1]
		return fmt.Sprintf("pci-%s-usb-0:%s:1.0-scsi-%s:%s:%s:%s",
			pciSlot, portpath, hctl[1], hctl[2], hctl[3], hctl[4])
	}
	for _, seg := range segments {
		if virtioSegmentPattern.MatchString(seg) {
			// The virtio segment names the device but contributes
			// nothing to the name beyond the PCI slot: a virtio-blk
			// device has no port structure of its own to describe, only
			// the PCI function QEMU or the hypervisor assigned it. This
			// matches udev's own path_id rule for virtio.
			return "pci-" + pciSlot
		}
	}

	return ""
}
