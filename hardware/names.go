package hardware

// Class words: this file turns bus class codes into the general
// kind that a fleet listing can print. Both tables are small,
// spec-defined enums: the PCI SIG's class codes and the USB-IF's
// interface classes. For this reason, they exist here as Go tables
// instead of a database file. The vendor and product names are the
// opposite case. They have tens of thousands of entries that change
// every month, and pci.ids exists for that case.

import "strings"

// pciClassWord decodes sysfs's class attribute (for example,
// "0x038000" means class 03, subclass 80, interface 00) to the word
// for its class byte. The vocabulary uses the PCI code space at
// base-class detail. An operator who decides whether to care about
// an unclaimed device needs a word like "display" or "network", not
// the subclass detail.
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

// usbClassWord decodes a USB interface's bInterfaceClass, two hex
// digits from the USB-IF's class code table, the same way.
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
