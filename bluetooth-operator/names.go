package main

// The identity of a published device.
//
// A controller's peer MAC address is the only name here that survives
// a reboot. The HID instance suffix in a sysfs directory name, the
// .0005 and the digits after it, is a counter that restarts at zero
// on every boot. The hci0:N connection handle changes on every
// reconnect. A claim written against either one allocates different
// hardware after the next boot, and the MAC does not move.
//
// A DRA device name must be a DNS label, and a colon is not a legal
// character in one, so the device name is the address in lowercase
// with dashes in place of the colons. The address itself publishes as
// an attribute, in the uppercase form that BlueZ prints and the label
// on the controller shows, so a selector can compare the address
// the way a person reads it.

import "strings"

// normalizeMAC returns an address in the one form this program keys
// on: lowercase, with colons. The two sources of an address disagree
// about case. BlueZ prints A0:AB:51:33:B7:12 over D-Bus, and the
// kernel prints HID_UNIQ as a0:ab:51:33:b7:12, so the two have to
// meet somewhere before a map lookup can pair them.
func normalizeMAC(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

// publishedMAC returns an address in the form the ResourceSlice
// uses: uppercase, with colons. `bluetoothctl devices` prints this
// form and the label on a controller shows it, so a DeviceClass or a
// claim that names one controller spells the address the way a person
// already has it written down.
func publishedMAC(address string) string {
	return strings.ToUpper(normalizeMAC(address))
}

// deviceName turns an address into the DNS label that a DRA device
// name must be. Lowercase and dashes are both required: the API
// rejects an uppercase letter and rejects a colon.
func deviceName(address string) string {
	return strings.ReplaceAll(normalizeMAC(address), ":", "-")
}

// macFromDeviceName inverts deviceName. The DRA prepare call supplies
// the allocated device's name and nothing else, so this is how the
// plugin reads which controller a claim holds.
func macFromDeviceName(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "-", ":")
}

// validMAC reports whether a string is six colon-separated pairs of
// hexadecimal digits. HID_UNIQ is empty on a HID device that has no
// peer address, and a device name from a prepare call is whatever the
// claim's allocation says, so both paths check the shape before they
// use it as an identity.
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
