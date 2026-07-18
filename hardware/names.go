package hardware

// Class words: turning bus class codes into the coarse kind a fleet
// listing can print. Both tables are small, spec-defined enums —
// the PCI SIG's class codes, the USB-IF's interface classes — so
// they live here as Go tables rather than as a database file. (The
// vendor and product names are the opposite case: tens of
// thousands of entries that change monthly, which is what pci.ids
// is for.)

import "strings"

// pciClassWord decodes sysfs's class attribute ("0x038000": class
// 03, subclass 80, interface 00) to the word for its class byte.
// The vocabulary is the PCI code space at base-class granularity —
// an operator deciding whether to care about an unclaimed device
// needs "display" or "network", not the subclass taxonomy.
func pciClassWord(class string) string {
	hex := strings.TrimPrefix(class, "0x")
	if len(hex) < 2 {
		return ""
	}
	switch hex[:2] {
	case "01":
		return "storage"
	case "02":
		return "network"
	case "03":
		return "display"
	case "04":
		return "multimedia"
	case "05":
		return "memory"
	case "06":
		return "bridge"
	case "07":
		return "communication"
	case "08":
		return "system"
	case "09":
		return "input"
	case "0c":
		return "serial-bus"
	case "0d":
		return "wireless"
	case "10":
		return "encryption"
	case "12":
		return "accelerator"
	}
	return ""
}

// usbClassWord decodes a USB interface's bInterfaceClass (two hex
// digits, from the USB-IF's class code table) the same way.
func usbClassWord(class string) string {
	switch strings.ToLower(class) {
	case "01":
		return "audio"
	case "02":
		return "communications"
	case "03":
		return "hid"
	case "06":
		return "imaging"
	case "07":
		return "printer"
	case "08":
		return "mass-storage"
	case "0a":
		return "cdc-data"
	case "0b":
		return "smart-card"
	case "0e":
		return "video"
	case "10":
		return "audio-video"
	case "e0":
		return "wireless"
	case "ff":
		return "vendor-specific"
	}
	return ""
}
