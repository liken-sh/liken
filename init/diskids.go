package main

// Stable identity names for local disks, computed from firmware
// values rather than kernel-assigned letters.
//
// A WWN (a world-unique ID number), a model string, and a serial
// number are values a disk's own controller reports over the wire.
// They live in the drive's firmware, not on its platters or its
// flash, so an image written onto a disk, or a whole disk cloned onto
// another, carries none of them: the copy still answers with its own
// controller's identity. That is what makes these values fit for
// naming a disk in a Machine spec. A kernel letter names a slot in
// probe order, which depends on which disk answered first, and a
// filesystem label is data on the disk, which an image write
// replaces.
//
// disklinks.go states why liken reads these values itself instead of
// running udev: everything udev publishes comes from files sysfs
// already exposes, so liken reads the same files. This holds here
// too. udev's by-id rules build several names from a disk's wwid,
// model, and serial attributes, in the sysfs directories this file
// reads.
//
// One udev by-id name is missing on purpose: ata-<model>_<serial>.
// The model string sysfs publishes for a SATA disk is the 16-byte
// SCSI INQUIRY string that libata's SCSI translation reports, not the
// 40-byte ATA IDENTIFY string udev's ata_id helper decodes straight
// from the drive. The two differ, so a name built from the shorter
// string would not match the name udev would have given the same
// disk, and a name that looks stable but points at the wrong string
// misleads more than an absent name does. wwn- already names every
// SATA disk that publishes a WWN. A SATA disk with no WWN gets only
// the by-path names diskpaths.go computes.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// diskIDNames lists the by-id names one disk answers to, in a fixed
// order: the WWN-derived names first, then the name specific to the
// disk's transport. The order is not significant to a reader of the
// names, but a fixed order keeps the tests, and any future diff of
// them, readable.
func diskIDNames(name string) []string {
	dir := filepath.Join(sysBlock, name)
	var names []string

	// NVMe publishes its own wwid at the block level; SCSI and SATA
	// disks publish it on the bus device underneath. Either location
	// can hold a NAA-format World Wide Name, the SCSI standard's
	// world-unique disk identifier, encoded as hex.
	wwid := sysfsString(dir, "wwid", "device/wwid")
	if hex, ok := strings.CutPrefix(wwid, "naa."); ok {
		hex = sanitizeIDPart(hex)
		// udev publishes both a wwn- name (its own convention) and a
		// scsi-3 name (the SCSI standard's own prefix for a NAA
		// identifier) for the same value, and other software looks
		// for either, so liken computes both.
		names = append(names, "wwn-0x"+hex, "scsi-3"+hex)
	}

	switch diskTransport(name) {
	case "nvme":
		model := sysfsString(dir, "device/model")
		serial := sysfsString(dir, "serial", "device/serial")
		if serial == "" {
			// A drive that answers SCSI inquiries under NVMe's SCSI
			// translation layer, or that a virtualized controller
			// exposes without a plain serial attribute, still answers
			// vital product data page 0x80 with its serial.
			serial = scsiVPD80Serial(dir)
		}
		if model != "" && serial != "" {
			names = append(names, "nvme-"+sanitizeIDPart(model)+"_"+sanitizeIDPart(serial))
		}
		if hex, ok := strings.CutPrefix(wwid, "eui."); ok {
			names = append(names, "nvme-eui."+sanitizeIDPart(hex))
		}
	case "virtio":
		// virtio-blk publishes only a serial, directly on the disk,
		// because a virtual disk has no model or vendor to report.
		if serial := sysfsString(dir, "serial"); serial != "" {
			names = append(names, "virtio-"+sanitizeIDPart(serial))
		}
	case "usb":
		if usb := usbIDName(dir); usb != "" {
			names = append(names, usb)
		}
	}

	return names
}

// usbIDName builds the by-id name for a disk that reaches liken over
// USB, from the manufacturer, product, and serial the USB device
// itself publishes, and the SCSI LUN usb-storage assigned it.
func usbIDName(dir string) string {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return ""
	}
	device := usbDeviceDir(real)
	if device == "" {
		return ""
	}
	manufacturer := sysfsString(device, "manufacturer")
	product := sysfsString(device, "product")
	serial := sysfsString(device, "serial")
	if manufacturer == "" || product == "" || serial == "" {
		return ""
	}
	return fmt.Sprintf("usb-%s_%s_%s-0:%s",
		sanitizeIDPart(manufacturer), sanitizeIDPart(product), sanitizeIDPart(serial),
		scsiLUN(real))
}

// usbDeviceDir walks a resolved sysfs path upward to the directory
// that names the USB device itself, as against the interface, the
// SCSI host, or the target beneath it that also sit on this path.
// idVendor exists only on the device's own directory, because only
// the device, not the things usb-storage attaches beneath it, has a
// USB vendor ID to report.
func usbDeviceDir(path string) string {
	for dir := path; dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "idVendor")); err == nil {
			return dir
		}
	}
	return ""
}

// hctlPattern matches a SCSI address segment in a sysfs path: host,
// channel, target, and LUN, each a plain number, the way the kernel
// names a SCSI device's directory.
var hctlPattern = regexp.MustCompile(`/(\d+):(\d+):(\d+):(\d+)(/|$)`)

// scsiLUN reads the LUN out of the h:c:t:l segment in a resolved
// sysfs path. usb-storage presents a USB mass-storage device to the
// kernel as a virtual SCSI host, and addresses each LUN under it as
// host:channel:target:lun, the way any SCSI host does; the final
// field is the LUN a multi-LUN device, such as a card reader with
// several slots, uses to tell its LUNs apart. A path with no such
// segment names LUN 0, the only LUN a device with no LUN addressing
// at all ever uses.
func scsiLUN(path string) string {
	if m := hctlPattern.FindStringSubmatch(path); m != nil {
		return m[4]
	}
	return "0"
}

// scsiVPD80Serial reads a disk's serial out of vital product data
// page 0x80, a binary SCSI page that every SCSI target, and every
// SATA disk through libata's SCSI translation, answers to. The page
// is four header bytes (peripheral qualifier and type, the page
// code, then the ASCII payload's length as a two-byte big-endian
// count) followed by the serial itself. diskIDNames reads this page
// as a fallback, for a disk whose driver does not also mirror the
// serial into a plain sysfs attribute.
func scsiVPD80Serial(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "device", "vpd_pg80"))
	if err != nil || len(raw) < 4 {
		return ""
	}
	length := int(raw[2])<<8 | int(raw[3])
	if len(raw) < 4+length {
		return ""
	}
	return strings.Trim(string(raw[4:4+length]), "\x00 \t\n\r\v\f")
}

// idCharset is every byte udev leaves alone when it builds a by-id
// name out of a firmware string. udev's own replace_whitespace and
// blacklist rules keep a name shell-safe and safe as a single path
// segment: a value must not smuggle a slash into the file name it
// becomes, or whitespace that would need quoting to reference again.
const idCharset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789#+.:=@_-"

// sanitizeIDPart makes one firmware string, such as a model or
// serial, safe to fold into a by-id name. Every byte outside
// idCharset, including the spaces a fixed-width SCSI field pads out
// with and the NUL bytes some firmware pads with instead, becomes an
// underscore.
func sanitizeIDPart(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if strings.IndexByte(idCharset, c) >= 0 {
			b.WriteByte(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
