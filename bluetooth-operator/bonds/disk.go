package bonds

// BlueZ's storage tree, read and written in the daemon's own shape.
//
// The tree under one adapter holds three kinds of entry, and only the
// first is a bond:
//
//   - a directory named after a paired device, holding that device's
//     info file, and often an attributes file that is empty.
//   - cache, a directory holding one file per device BlueZ has seen on
//     the air. These are the neighbours' phones and headsets. They
//     carry no key, this machine has no claim on them, and copying
//     them into the API would publish who lives nearby.
//   - settings, the adapter's own power state, which the pod already
//     states in bluetoothd's main.conf.
//
// Only the info files travel. An entry whose name does not parse as an
// address is skipped rather than fatal, because a later BlueZ may
// write more beside the bonds and an unknown name is not a failure.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// The modes BlueZ writes its own tree with. An info file holds the
	// link key, which is the whole of what a person needs to open a
	// session as that device, so nothing but the owner reads it.
	bondDirMode  = 0o700
	bondFileMode = 0o600

	// infoFile is the one file per device that carries the bond.
	infoFile = "info"
)

// ReadTree reads one adapter's bonds out of a BlueZ storage tree.
//
// An adapter with no directory is an empty tree and not an error. That
// is the state of a machine that has paired nothing, and it is also
// the state of a machine where bluetoothd has started and no pairing
// has happened yet.
func ReadTree(root string, adapter Address) (Tree, error) {
	directory := filepath.Join(root, adapter.Directory())
	entries, err := os.ReadDir(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return Tree{}, nil
	}
	if err != nil {
		return nil, err
	}

	tree := Tree{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		device, err := ParseAddress(entry.Name())
		if err != nil {
			continue
		}
		// An info file that is missing, unreadable, or empty is a
		// pairing that did not finish. Writing it back would give
		// bluetoothd a device directory with no key in it, which is a
		// device that can never connect and that no unpairing removes.
		info, err := os.ReadFile(filepath.Join(directory, entry.Name(), infoFile))
		if err != nil || len(info) == 0 {
			continue
		}
		tree[device] = info
	}
	return tree, nil
}

// WriteTree writes the bonds into a BlueZ storage tree, so that
// bluetoothd finds them at the path it reads.
//
// It adds to whatever is already there and removes nothing. The
// caller is the init container, which writes into an empty directory
// before bluetoothd starts.
func WriteTree(root string, adapter Address, tree Tree) error {
	directory := filepath.Join(root, adapter.Directory())
	if err := os.MkdirAll(directory, bondDirMode); err != nil {
		return err
	}
	for device, info := range tree {
		deviceDirectory := filepath.Join(directory, device.Directory())
		if err := os.MkdirAll(deviceDirectory, bondDirMode); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(deviceDirectory, infoFile), info, bondFileMode); err != nil {
			return err
		}
	}
	return nil
}
