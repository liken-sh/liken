// Package bonds stores an adapter's link keys in Kubernetes, so they
// survive the loss of the pod that paired the device.
//
// A bond is the link key that pairing creates between one adapter and
// one device, and every later connection between them uses it. The
// operator cannot recreate a bond from the cluster: a lost bond means
// that a person pairs the controller again by hand.
//
// bluetoothd keeps bonds under /var/lib/bluetooth, in one directory per
// adapter, with one directory per paired device inside it and a cache
// directory with an entry for each device the adapter has seen. This
// package reads and writes that tree in bluetoothd's own format,
// because bluetoothd cannot store bonds anywhere else.
//
// The operator stores each bond in its own Secret, labelled with the
// adapter's Bluetooth address. The Secrets are found by that address,
// not by node name or StatefulSet ordinal, because a link key belongs
// to one radio, and the radio can move to another machine, be replaced,
// or come back at a different hci index after a USB reset. A Secret
// found by node name would give a machine with a new adapter the keys
// of a radio it no longer has.
package bonds

import (
	"bytes"
)

// Tree is one adapter's bonds: the files of each paired device, keyed
// by the device's address.
type Tree map[Address]Files

// Files holds the two files bluetoothd keeps for one paired device.
// This package copies both byte for byte and parses neither.
//
// Info is <adapter>/<device>/info. It has the link key, the device
// class, and the services BlueZ recorded. A device with no info file is
// not a bond.
//
// Cache is <adapter>/cache/<device>. Its [ServiceRecords] section has
// the SDP records that BlueZ read from the device. A BR/EDR HID device
// does not reconnect without them, because bluetoothd does not run SDP
// discovery again for a bonded device, and the input profile reads the
// HID record from this file to set up the connection. Cache is empty
// when BlueZ wrote no cache entry for the device, which happens when
// the device paired before BlueZ resolved its name.
type Files struct {
	Info  []byte
	Cache []byte
}

// Equal reports whether both files are identical. An empty Cache
// differs from a written one, so when BlueZ writes the cache entry after
// pairing, the operator writes the Secret again.
func (f Files) Equal(other Files) bool {
	return bytes.Equal(f.Info, other.Info) && bytes.Equal(f.Cache, other.Cache)
}

// Merge copies the bonds of other into t, and replaces a device that t
// already has.
func (t Tree) Merge(other Tree) {
	for device, files := range other {
		t[device] = files
	}
}
