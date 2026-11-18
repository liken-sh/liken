package bonds

// This file defines Address, which converts between the two text forms
// of a Bluetooth address.
//
// BlueZ names the directories of its storage tree in uppercase with
// colons, as /var/lib/bluetooth/04:4A:69:66:92:27. bluetoothctl and
// the label on the hardware use the same form. A Kubernetes object
// name allows no uppercase letters or colons, and a Secret key allows
// no colons, so the operator writes lowercase with dashes on the API
// side, as 04-4a-69-66-92-27. Its DRA device names use the same form.
//
// Address stores the six octets, so the same address parsed from the
// kernel, a directory name, or a Secret key compares equal.

import (
	"fmt"
	"strconv"
	"strings"
)

// Address is a Bluetooth address as six octets, most significant first,
// which is the order a person reads them. The kernel stores the octets
// in the opposite order, so a caller that reads one from a kernel
// structure must reverse them first.
type Address [6]byte

// ParseAddress parses an address in either text form, in either case.
// It returns an error for anything that is not six two-digit
// hexadecimal octets, so a caller that walks BlueZ's storage tree can
// skip entries such as cache and settings.
func ParseAddress(text string) (Address, error) {
	var address Address
	octets := strings.Split(strings.ReplaceAll(text, "-", ":"), ":")
	if len(octets) != len(address) {
		return Address{}, fmt.Errorf("%q is not a Bluetooth address: it has %d octets, not %d", text, len(octets), len(address))
	}
	for i, octet := range octets {
		// ParseUint accepts one digit. Without this check,
		// "4:4A:69:66:92:27" would parse, and its Directory would not
		// match the name it came from.
		if len(octet) != 2 {
			return Address{}, fmt.Errorf("%q is not a Bluetooth address: %q is not two hexadecimal digits", text, octet)
		}
		value, err := strconv.ParseUint(octet, 16, 8)
		if err != nil {
			return Address{}, fmt.Errorf("%q is not a Bluetooth address: %q is not two hexadecimal digits", text, octet)
		}
		address[i] = byte(value)
	}
	return address, nil
}

// Directory returns the directory name BlueZ uses for this address in
// its storage tree: uppercase with colons.
func (a Address) Directory() string {
	return strings.ToUpper(a.format(":"))
}

// Key returns the form that a Kubernetes name and a Secret key use:
// lowercase with dashes.
func (a Address) Key() string {
	return a.format("-")
}

// String returns the Directory form, the one a person reads on the
// label and in bluetoothctl.
func (a Address) String() string {
	return a.Directory()
}

// IsZero reports whether every octet is zero. The kernel reports the
// all-zero address for an adapter after it registers the adapter and
// before its queued power-on work reads the real address from the
// controller.
func (a Address) IsZero() bool {
	return a == Address{}
}

func (a Address) format(separator string) string {
	octets := make([]string, len(a))
	for i, octet := range a {
		octets[i] = fmt.Sprintf("%02x", octet)
	}
	return strings.Join(octets, separator)
}
