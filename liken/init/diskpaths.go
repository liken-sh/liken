package main

// This file computes the by-path name of a local disk, the same name
// that udev's path_id builtin gives it under /dev/disk/by-path, for
// disks on SATA, NVMe, USB, virtio, and mmc.
//
// A by-id name follows the disk, and a by-path name follows the port
// the disk is plugged into. So a spec that gives a by-path name selects
// whatever disk is in that bay, including a spare swapped in after a
// failure. diskids.go builds the by-id names.
//
// path_id builds the name from the parent devices between the disk and
// its bus. The kernel lists those parents as the directories of the
// resolved path of /sys/block/<name>, and this file reads that path.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// pciSlotPattern matches a PCI address directory such as 0000:00:1f.2
// (domain:bus:device.function). A disk's path can contain several, for
// example a root port and the storage controller behind it, so
// diskPathName keeps the last one, which is closest to the disk.
var pciSlotPattern = regexp.MustCompile(`^[0-9a-fA-F]{4}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2}\.[0-9a-fA-F]$`)

// ataSegmentPattern matches the ataN directory that libata creates for
// each port.
var ataSegmentPattern = regexp.MustCompile(`^ata(\d+)$`)

var virtioSegmentPattern = regexp.MustCompile(`^virtio\d+$`)

// usbPortSegmentPattern matches a USB device directory: the bus number,
// a dash, and the port path through any hubs, such as 1-2.4. It does
// not match the interface directory below it, such as 1-2.4:1.0.
var usbPortSegmentPattern = regexp.MustCompile(`^\d+-[0-9.]+$`)

// iscsiSessionPattern matches the sessionN directory that
// scsi_transport_iscsi creates for each login. The session number
// changes on every login, so diskPathName gives an iSCSI disk no name.
// disklinks.go builds a stable by-path name for an iSCSI disk from its
// target and address.
var iscsiSessionPattern = regexp.MustCompile(`^session\d+$`)

// nvmeNsidPattern matches the trailing number of an NVMe block device
// name, the 1 in nvme0n1. diskPathName uses it only on a kernel with no
// nsid attribute in sysfs, because the kernel numbers block devices in
// probe order. The number equals the namespace ID only when the
// controller's namespaces start at 1 and have no gaps.
var nvmeNsidPattern = regexp.MustCompile(`n(\d+)$`)

// mmcHostSegment is the directory that the mmc core creates directly
// below the host controller's device directory.
const mmcHostSegment = "mmc_host"

// mmcPathName returns the by-path name of an eMMC or SD card, or ""
// when udev gives it none. segments is the resolved sysfs path split
// into directories, and pciSlot is the PCI address that diskPathName
// found in it, or "".
//
// path_id has no handler for mmc, so a card's name comes from its host
// controller alone. A controller that ACPI or a device tree describes
// is a platform device, and path_id names it platform-<name>, with
// pci-<slot>- in front when it is below a PCI function.
//
// A controller that is itself a PCI function, as with sdhci-pci, has no
// platform device. path_id counts a PCI slot as a parent but not as a
// transport, and it writes no ID_PATH for a disk with no transport, so
// udev makes no by-path link. mmcPathName returns "" there too, so that
// every name matches udev's. The card still has a by-id name.
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

// diskPathName returns the by-path name of the disk /sys/block/<name>.
// It returns "" for an iSCSI disk, a disk on a bus this file does not
// handle, a USB disk with no SCSI address in its path, and a disk whose
// /sys/block entry does not resolve.
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
			// path_id reads the nsid attribute.
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
			// A virtio-blk disk has no port, so its name is the PCI
			// slot alone, as in path_id.
			return "pci-" + pciSlot
		}
	}

	return ""
}
