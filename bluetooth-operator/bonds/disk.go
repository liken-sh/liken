package bonds

// BlueZ's storage tree, read and written in the daemon's own shape.
//
// The tree under one adapter holds three kinds of entry:
//
//   - a directory named after a paired device, holding that device's
//     info file, and often an attributes file that is empty. The info
//     file is the bond.
//   - cache, a directory holding one file per device BlueZ has
//     resolved a name for. That is every device the radio detected,
//     so most of these entries are the neighbours' phones and headsets.
//   - settings, the adapter's own power state, which the pod already
//     states in bluetoothd's main.conf.
//
// The device directories and the cache entries that match them travel,
// and nothing else does. A cache entry whose device has a directory
// holds the SDP records, and a BR/EDR HID device does not reconnect
// without them. A cache entry with no device directory is a device
// this adapter never paired with, and copying it would publish who
// lives nearby, so the device directory is the test.
//
// An entry whose name does not parse as an address is skipped rather
// than fatal, because a later BlueZ may write more beside the bonds
// and an unknown name is not a failure.

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

	// infoFile is the file under each device's own directory that
	// holds the bond, and cacheDirectory is where the adapter keeps
	// one file per device it has resolved a name for, named by the
	// device's address.
	infoFile       = "info"
	cacheDirectory = "cache"
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
		// An info file that is missing or empty is a pairing that did
		// not finish. Writing it back would give bluetoothd a device
		// directory with no key in it, which is a device that can never
		// connect and that no unpairing removes. Any other read failure
		// fails the whole call. The info file is the bond, and a read
		// that fails on a file this process owns means something already
		// went wrong. The caller compares this tree against the Secret
		// and writes the difference, so a silent skip would write a
		// Secret with this bond missing, which is the loss the Secret
		// exists to prevent.
		info, err := os.ReadFile(filepath.Join(directory, entry.Name(), infoFile))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if len(info) == 0 {
			continue
		}
		// The cache entry takes the same name BlueZ gave the device's
		// directory. A device that paired before name resolution
		// finished has no cache entry yet, and that is a bond with one
		// file. Any other read failure fails the whole call, because
		// the caller compares this tree against the Secret and writes
		// the difference, so a cache file this misses is a cache file
		// the next write drops.
		cache, err := os.ReadFile(filepath.Join(directory, cacheDirectory, entry.Name()))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		tree[device] = Files{Info: info, Cache: cache}
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
	for device, stored := range tree {
		deviceDirectory := filepath.Join(directory, device.Directory())
		if err := os.MkdirAll(deviceDirectory, bondDirMode); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(deviceDirectory, infoFile), stored.Info, bondFileMode); err != nil {
			return err
		}
		// A device with no stored cache entry gets no file. An empty
		// one would give bluetoothd a key file with no
		// [ServiceRecords] group, which reads as a device with no
		// service records, and bluetoothd would not run the discovery
		// that fills it in.
		if len(stored.Cache) == 0 {
			continue
		}
		cache := filepath.Join(directory, cacheDirectory)
		if err := os.MkdirAll(cache, bondDirMode); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(cache, device.Directory()), stored.Cache, bondFileMode); err != nil {
			return err
		}
	}
	return nil
}
