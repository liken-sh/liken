package main

// This file names the devices that the operator publishes.
//
// A controller's MAC address is the only identifier that survives a
// reboot. The HID instance suffix in a sysfs directory name, such as
// .0005, is a counter that restarts at zero on every boot. The hci0:N
// connection handle changes on every reconnect. A claim that selects
// by either one can get a different controller after the next boot.

import "strings"

// normalizeMAC returns the address in lowercase with colons, the form
// this program uses as a map key. BlueZ sends an address over D-Bus in
// uppercase, and HID_UNIQ can be lowercase, so both are normalized
// before they are compared.
func normalizeMAC(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

// publishedMAC returns the address in uppercase with colons, the form
// of the `address` attribute in the ResourceSlice. `bluetoothctl
// devices` prints this form and the label on a controller shows it, so
// a person can copy it into a DeviceClass or claim selector.
func publishedMAC(address string) string {
	return strings.ToUpper(normalizeMAC(address))
}

// deviceName returns the DRA device name for an address: lowercase
// with dashes. A device name must be a DNS label, which allows no
// uppercase letters and no colons.
func deviceName(address string) string {
	return strings.ReplaceAll(normalizeMAC(address), ":", "-")
}

// macFromDeviceName converts a device name back to a normalized
// address. The DRA prepare call receives only the allocated device's
// name, so the plugin finds the controller from the name.
func macFromDeviceName(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "-", ":")
}

// mediaBusSuffix is the end of the media bus device's name. The bus
// name is the adapter's address in device-name form plus this suffix.
// A controller's device name is six octets with no suffix, so the two
// cannot collide.
const mediaBusSuffix = "-media"

// mediaBusName returns the media bus device's name for the adapter's
// address.
func mediaBusName(address string) string {
	return deviceName(address) + mediaBusSuffix
}

// isMediaBusName reports whether a device name is the media bus's.
func isMediaBusName(name string) bool {
	return strings.HasSuffix(name, mediaBusSuffix)
}

// validMAC reports whether the string is six colon-separated pairs of
// hexadecimal digits. HID_UNIQ is empty on a HID device that has no
// peer address, and a device name from a prepare call can be any
// string, so callers check the shape before they use either as an
// address.
func validMAC(address string) bool {
	octets := strings.Split(normalizeMAC(address), ":")
	if len(octets) != 6 {
		return false
	}
	for _, octet := range octets {
		if len(octet) != 2 {
			return false
		}
		for _, c := range octet {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return false
			}
		}
	}
	return true
}
