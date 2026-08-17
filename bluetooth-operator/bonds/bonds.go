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
// paired device below it. The tree is the daemon's, so this package
// reads and writes it in the daemon's own shape, and never asks
// bluetoothd to store them anywhere else.
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

// Tree is one adapter's stored bonds: each paired device's info file,
// keyed by that device's address.
//
// The info file is the whole of what this package carries. It holds
// the link key, the device's class, and the services BlueZ recorded,
// which is what bluetoothd needs to accept a connection from that
// device without a new pairing.
type Tree map[Address][]byte

// Same reports whether two trees hold the same devices with the same
// contents. The operator writes the Secret only when this says no,
// because bluetoothd rewrites its tree for reasons that change no key,
// and a write on every pass would be a write to the API server on
// every pass.
func (t Tree) Same(other Tree) bool {
	return maps.EqualFunc(t, other, bytes.Equal)
}
