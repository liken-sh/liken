package main

// This file builds the same /dev/disk/by-id names that udev would,
// from the sysfs files udev reads, because liken does not run udev
// (disklinks.go). The names come from the WWN, model, and serial in
// the drive's firmware. An image write or a whole-disk clone does not
// change them, so a Machine spec can name a disk by them. A kernel
// name such as sda depends on probe order, and an image write replaces
// a filesystem label.
//
// For a SATA disk, the sysfs model attribute is the 16-byte SCSI
// INQUIRY model from libata, but udev's ata_id takes the 40-byte model
// from ATA IDENTIFY DEVICE. So the ata- name reads IDENTIFY from VPD
// page 0x89, where libata exposes the whole 512-byte block.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// diskIDNames returns the WWN names first, then the transport's name.
func diskIDNames(name string) []string {
	dir := filepath.Join(sysBlock, name)
	var names []string

	// NVMe has wwid on the block device, and SCSI and SATA have it on
	// the device under it.
	wwid := sysfsString(dir, "wwid", "device/wwid")
	if hex, ok := strings.CutPrefix(wwid, "naa."); ok {
		hex = sanitizeIDPart(hex)
		// udev creates both links for a NAA WWN.
		names = append(names, "wwn-0x"+hex, "scsi-3"+hex)
	}

	switch diskTransport(name) {
	case "sata":
		if model, serial := scsiVPD89ATAIdentity(dir); model != "" && serial != "" {
			names = append(names, "ata-"+sanitizeIDPart(model)+"_"+sanitizeIDPart(serial))
		}
	case "nvme":
		model := sysfsString(dir, "device/model")
		serial := sysfsString(dir, "serial", "device/serial")
		if serial == "" {
			// Some drives have no serial attribute but report the
			// serial in VPD page 0x80.
			serial = scsiVPD80Serial(dir)
		}
		if model != "" && serial != "" {
			names = append(names, "nvme-"+sanitizeIDPart(model)+"_"+sanitizeIDPart(serial))
		}
		if hex, ok := strings.CutPrefix(wwid, "eui."); ok {
			names = append(names, "nvme-eui."+sanitizeIDPart(hex))
		}
	case "mmc":
		// The name and serial come from the card's CID register. The
		// kernel prints the serial with a 0x prefix, and udev's name
		// keeps it.
		card := sysfsString(dir, "device/name")
		serial := sysfsString(dir, "device/serial")
		if card != "" && serial != "" {
			names = append(names, "mmc-"+sanitizeIDPart(card)+"_"+sanitizeIDPart(serial))
		}
	case "virtio":
		// A virtio-blk disk has a serial but no model or vendor.
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

// usbIDName returns "" when the USB device has no manufacturer,
// product, or serial.
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

// usbDeviceDir returns the nearest parent of a resolved sysfs path
// that has idVendor. Of the USB device, interface, SCSI host, and
// target directories on the path, only the USB device has it.
func usbDeviceDir(path string) string {
	for dir := path; dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "idVendor")); err == nil {
			return dir
		}
	}
	return ""
}

// hctlPattern matches a SCSI host:channel:target:lun directory.
var hctlPattern = regexp.MustCompile(`/(\d+):(\d+):(\d+):(\d+)(/|$)`)

// scsiLUN tells apart the disks of one USB device with several LUNs,
// such as a card reader with several slots.
func scsiLUN(path string) string {
	if m := hctlPattern.FindStringSubmatch(path); m != nil {
		return m[4]
	}
	return "0"
}

// scsiVPD80Serial reads the serial from VPD page 0x80: a four-byte
// header whose last two bytes are the big-endian length, then the
// serial.
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

// scsiVPD89ATAIdentity reads VPD page 0x89: a 60-byte header, then
// the 512-byte IDENTIFY DEVICE block. In IDENTIFY, the serial is words
// 10-19 and the model is words 27-46.
func scsiVPD89ATAIdentity(dir string) (model, serial string) {
	raw, err := os.ReadFile(filepath.Join(dir, "device", "vpd_pg89"))
	if err != nil || len(raw) < 572 {
		return "", ""
	}
	identify := raw[60:572]
	serial = ataIdentifyString(identify[20:40])
	model = ataIdentifyString(identify[54:94])
	return model, serial
}

// ataIdentifyString undoes the byte swap of IDENTIFY, which stores
// each 16-bit word of a string with its two bytes reversed, so that
// "QEMU HARDDISK" reads "EQUMH RADDSI K".
func ataIdentifyString(field []byte) string {
	swapped := make([]byte, len(field))
	for i := 0; i+1 < len(field); i += 2 {
		swapped[i], swapped[i+1] = field[i+1], field[i]
	}
	return strings.Trim(string(swapped), "\x00 \t\n\r\v\f")
}

// idCharset is the set of bytes that udev keeps in a by-id name. udev
// replaces every other byte, such as a slash or a space, so that the
// name is one safe path segment.
const idCharset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789#+.:=@_-"

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
