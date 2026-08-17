// Package bonds keeps an adapter's link keys where they outlive the
// pod that made them.
//
// A bond is what pairing produces: a key that one adapter and one
// device share, which every later connection between the two uses. It
// is the only state in this operator that a person cannot recreate
// from the cluster, because recreating it means holding the
// controller and pressing its pairing buttons again.
//
// bluetoothd keeps the bonds in a directory tree under
// /var/lib/bluetooth, one directory per adapter and one directory per
// paired device below it, with a cache directory the adapter's devices
// share. The tree is the daemon's, so this package reads and writes it
// in the daemon's own shape, and never asks bluetoothd to store them
// anywhere else.
//
// The copy that survives the pod lives in a Kubernetes Secret, keyed
// by the adapter's Bluetooth address. The address is what identifies
// the keys: a key belongs to one radio, and the radio can move to
// another machine, be replaced on the same machine, or come back at a
// different index after a USB reset. Anything else that could name the
// Secret, such as a node name or a StatefulSet ordinal, names the
// machine rather than the radio, and a machine whose adapter was
// swapped would then be handed keys that belong to a radio it no
// longer has.
package bonds

import (
	"bytes"
	"maps"
)

// Tree is one adapter's stored bonds: each paired device's files,
// keyed by that device's address.
type Tree map[Address]Files

// Files is what this package carries for one paired device. Both files
// are BlueZ's own, byte for byte, and nothing here parses either one.
//
// Info is <adapter>/<device>/info. It holds the link key, the device's
// class, and the list of services BlueZ recorded. A device with no
// info file is not a bond.
//
// Cache is <adapter>/cache/<device>. It holds the SDP records BlueZ
// read from the device, under [ServiceRecords]. A BR/EDR HID device
// does not reconnect without them, because bluetoothd runs no new SDP
// discovery for a device it already holds a bond for, and the input
// profile parses the HID SDP record out of this file to bring the
// connection up. Cache is empty for a device BlueZ has written no
// cache entry for, which is a device paired before name resolution
// finished.
type Files struct {
	Info  []byte
	Cache []byte
}

// Equal reports whether two devices hold the same two files. A device
// that has one file and a device that has both are different, so a
// cache entry that arrives after the pairing reaches the Secret.
func (f Files) Equal(other Files) bool {
	return bytes.Equal(f.Info, other.Info) && bytes.Equal(f.Cache, other.Cache)
}

// Merge copies another tree's bonds into this one. The init container
// reads one radio's bonds out of several Secrets, so it merges them,
// and a device that appears in two of them takes the value from the
// tree merged last.
func (t Tree) Merge(other Tree) {
	for device, files := range other {
		t[device] = files
	}
}

// Same reports whether two trees hold the same devices with the same
// contents. The operator writes the Secret only when this says no,
// because bluetoothd rewrites its tree for reasons that change no key,
// and a write on every pass would be a write to the API server on
// every pass.
func (t Tree) Same(other Tree) bool {
	return maps.EqualFunc(t, other, Files.Equal)
}
