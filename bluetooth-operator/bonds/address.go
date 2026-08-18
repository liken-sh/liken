package bonds

// The two forms a Bluetooth address takes here, and why there are two.
//
// BlueZ writes its storage tree with the address uppercase and
// separated by colons, as /var/lib/bluetooth/04:4A:69:66:92:27. That
// is also how the label on the hardware reads and how bluetoothctl
// prints it.
//
// A Kubernetes object name and a Secret key accept neither an
// uppercase letter nor a colon, so the API side spells the same
// address lowercase and separated by dashes, as 04-4a-69-66-92-27.
// This is the convention the operator's DRA device names already
// follow.
//
// Address holds the octets themselves rather than either string, so
// that a value from the kernel, a value from a directory name, and a
// value from a Secret key all compare and hash as the same address.

import (
	"fmt"
	"strconv"
	"strings"
)

// Address is one Bluetooth address: six octets in the order a person
// reads them, most significant first. The kernel reports the same
// octets in the opposite order, so a caller that reads one out of a
// kernel structure reverses them first.
type Address [6]byte

// ParseAddress reads an address in either of the two forms, in either
// case. Anything that is not six hexadecimal octets is an error, which
// is what lets a caller walk BlueZ's tree and skip the entries that
// are not devices: cache and settings both fail here.
func ParseAddress(text string) (Address, error) {
	var address Address
	octets := strings.Split(strings.ReplaceAll(text, "-", ":"), ":")
	if len(octets) != len(address) {
		return Address{}, fmt.Errorf("%q is not a Bluetooth address: it has %d octets, not %d", text, len(octets), len(address))
	}
	for i, octet := range octets {
		// The length check comes first because strconv accepts a single
		// digit, and an address with a one-digit octet is a name this
		// package must not turn into a different address.
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

// Directory returns the name BlueZ gives this address in its storage
// tree: uppercase, separated by colons.
func (a Address) Directory() string {
	return strings.ToUpper(a.format(":"))
}

// Key returns the form a Kubernetes name and a Secret key use:
// lowercase, separated by dashes.
func (a Address) Key() string {
	return a.format("-")
}

// String prints the address the way BlueZ prints it and the way the
// label on the hardware reads, so a message about an adapter names it
// the way a person already has it written down.
func (a Address) String() string {
	return a.Directory()
}

// IsZero reports whether every octet is zero. The kernel answers with
// this address between the moment it registers an adapter and the
// moment its queued power-on work reads the real address out of the
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
