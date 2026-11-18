package bonds

// Reads and writes bonds in BlueZ's storage directory. Under one
// adapter's directory, BlueZ keeps:
//
//   - a directory for each paired device, named by its address, with
//     an info file that holds the bond.
//   - cache, with a file for each device whose name BlueZ has resolved.
//     That includes every device the radio has detected, so most
//     entries belong to other people's phones and headsets.
//   - settings, the adapter's power state. It is not copied, because
//     bluetoothd's main.conf sets AutoEnable, which powers the adapter
//     on at start.
//   - identity, the adapter's identity resolving key. It exists only
//     after privacy has been on, and the operator stores it in its own
//     Secret (identity.go).
//
// This package copies the device directories and the cache file of
// each paired device. A cache file holds the device's SDP records, and
// a BR/EDR HID device does not reconnect without them. The cache files
// of unpaired devices are not copied, so the Secret holds no record of
// the neighbours' devices.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// The modes that BlueZ uses for its own tree. An info file holds the
	// link key, which lets anyone who has it connect as that device, so
	// only the owner may read it.
	bondDirMode  = 0o700
	bondFileMode = 0o600

	infoFile       = "info"
	cacheDirectory = "cache"
)

// ReadTree reads the bonds of one adapter from a BlueZ storage tree.
// An adapter with no directory has paired nothing, and returns an empty
// tree. ReadTree skips an entry whose name is not an address, so a
// newer BlueZ that writes other files there does not make it fail.
//
// Any read error other than a missing file is returned. The caller
// writes the difference between this tree and the Secret, so a bond
// that ReadTree skipped because it could not be read would be deleted
// from the Secret.
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
		// A missing or empty info file is a pairing that did not finish.
		// Written back, it would give bluetoothd a device directory with
		// no key, which can never connect and which unpairing does not
		// remove.
		info, err := os.ReadFile(filepath.Join(directory, entry.Name(), infoFile))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if len(info) == 0 {
			continue
		}
		// A device that paired before BlueZ resolved its name has no
		// cache file yet, and is still a valid bond.
		cache, err := os.ReadFile(filepath.Join(directory, cacheDirectory, entry.Name()))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		tree[device] = Files{Info: info, Cache: cache}
	}
	return tree, nil
}

// WriteTree writes the bonds into a BlueZ storage tree. It deletes no
// file, because bondfetch calls it before bluetoothd starts, when the
// directory is empty.
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
		// An empty cache file has no [ServiceRecords] group, so
		// bluetoothd would read it as a device with no service records
		// and would not run the discovery that fills them in.
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
