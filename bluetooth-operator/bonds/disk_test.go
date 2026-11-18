package bonds

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestReadTreeKeepsOnlyThePairedDevices(t *testing.T) {
	root := blueZTree(t)

	tree, err := ReadTree(root, address(t, testAdapter))
	if err != nil {
		t.Fatalf("ReadTree returned %v, want no error", err)
	}
	// cache/ has an entry for the paired device and one for a
	// neighbour's device, which has no device directory.
	if len(tree) != 1 {
		t.Fatalf("the tree has %d bonds, want 1: %v", len(tree), tree)
	}
	device, ok := tree[address(t, testDevice)]
	if !ok {
		t.Fatalf("the tree is %v, want it to hold the paired device", tree)
	}
	if string(device.Info) != testInfo {
		t.Errorf("info = %q, want %q", device.Info, testInfo)
	}
}

// The cache file holds the SDP records. Without them, a BR/EDR HID
// device does not reconnect: bluetoothd logs "Could not parse HID SDP
// record" and the controller drops the connection at once.
func TestReadTreeCarriesTheCacheEntryOfAPairedDevice(t *testing.T) {
	tree, err := ReadTree(blueZTree(t), address(t, testAdapter))
	if err != nil {
		t.Fatalf("ReadTree returned %v, want no error", err)
	}

	if got := string(tree[address(t, testDevice)].Cache); got != testCache {
		t.Errorf("cache = %q, want %q", got, testCache)
	}
}

// A neighbour's phone has a cache entry and no device directory.
// ReadTree returns nothing about it, under either address.
func TestReadTreeLeavesTheCacheEntriesOfUnpairedDevices(t *testing.T) {
	tree, err := ReadTree(blueZTree(t), address(t, testAdapter))
	if err != nil {
		t.Fatalf("ReadTree returned %v, want no error", err)
	}

	if _, found := tree[address(t, testNeighbour)]; found {
		t.Fatalf("the tree is %v, want no device that this adapter never paired with", tree)
	}
	for device, stored := range tree {
		if string(stored.Cache) == testNeighbourCache {
			t.Errorf("the bond of %s holds the neighbour's cache entry, want none", device)
		}
	}
}

// bluetoothd writes the cache entry when it resolves the name, so a
// device paired by an incoming connection can have no entry yet. That
// is a valid bond with one file, not a read error.
func TestReadTreeAcceptsAPairedDeviceWithNoCacheEntry(t *testing.T) {
	root := blueZTree(t)
	if err := os.Remove(filepath.Join(root, testAdapter, "cache", testDevice)); err != nil {
		t.Fatalf("removing the cache entry: %v", err)
	}

	tree, err := ReadTree(root, address(t, testAdapter))
	if err != nil {
		t.Fatalf("ReadTree returned %v, want no error", err)
	}

	device, ok := tree[address(t, testDevice)]
	if !ok {
		t.Fatalf("the tree is %v, want it to hold the paired device", tree)
	}
	if string(device.Info) != testInfo {
		t.Errorf("info = %q, want %q", device.Info, testInfo)
	}
	if len(device.Cache) != 0 {
		t.Errorf("cache = %q, want none", device.Cache)
	}
}

func TestReadTreeSkipsEntriesThatAreNotBonds(t *testing.T) {
	root := blueZTree(t)
	adapter := filepath.Join(root, testAdapter)

	// A directory named by an address with no info file. BlueZ leaves
	// one when a pairing does not complete.
	if err := os.MkdirAll(filepath.Join(adapter, "AA:BB:CC:DD:EE:FF"), 0o700); err != nil {
		t.Fatalf("creating the directory of a bond with no info file: %v", err)
	}
	// An empty info file has no link key.
	if err := os.MkdirAll(filepath.Join(adapter, "AA:BB:CC:DD:EE:00"), 0o700); err != nil {
		t.Fatalf("creating the directory of a bond with an empty info file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(adapter, "AA:BB:CC:DD:EE:00", "info"), nil, 0o600); err != nil {
		t.Fatalf("writing the empty info file: %v", err)
	}

	tree, err := ReadTree(root, address(t, testAdapter))
	if err != nil {
		t.Fatalf("ReadTree returned %v, want no error", err)
	}
	if len(tree) != 1 {
		t.Fatalf("the tree has %d bonds, want 1: %v", len(tree), tree)
	}
}

// Skipping a bond whose info file cannot be read would delete it from
// the Secret. The test replaces the info file with a directory, which
// os.ReadFile rejects with an error other than ENOENT.
func TestReadTreeFailsOnAnUnreadableInfoFile(t *testing.T) {
	root := blueZTree(t)
	info := filepath.Join(root, testAdapter, testDevice, "info")
	if err := os.Remove(info); err != nil {
		t.Fatalf("removing the info file: %v", err)
	}
	if err := os.Mkdir(info, 0o700); err != nil {
		t.Fatalf("creating a directory in place of the info file: %v", err)
	}

	if _, err := ReadTree(root, address(t, testAdapter)); err == nil {
		t.Fatal("ReadTree returned no error for an info file that cannot be read, want an error")
	}
}

// An adapter that has paired nothing has no directory, as on a
// machine's first start.
func TestReadTreeWithNoDirectory(t *testing.T) {
	tree, err := ReadTree(t.TempDir(), address(t, testAdapter))
	if err != nil {
		t.Fatalf("ReadTree returned %v, want no error", err)
	}
	if len(tree) != 0 {
		t.Fatalf("the tree is %v, want empty", tree)
	}
}

func TestWriteTreeWritesWhereBlueZReads(t *testing.T) {
	root := t.TempDir()
	tree := Tree{address(t, testDevice): files(testInfo, testCache)}

	if err := WriteTree(root, address(t, testAdapter), tree); err != nil {
		t.Fatalf("WriteTree returned %v, want no error", err)
	}

	// These are BlueZ's paths: uppercase addresses with colons, the info
	// file under the adapter and device directories, and the cache file
	// in a directory shared by all the adapter's devices.
	cases := []struct {
		path string
		want string
	}{
		{path: filepath.Join("04:4A:69:66:92:27", "7C:66:EF:22:E7:80", "info"), want: testInfo},
		{path: filepath.Join("04:4A:69:66:92:27", "cache", "7C:66:EF:22:E7:80"), want: testCache},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			contents, err := os.ReadFile(filepath.Join(root, c.path))
			if err != nil {
				t.Fatalf("reading %s: %v", c.path, err)
			}
			if string(contents) != c.want {
				t.Errorf("%s holds %q, want %q", c.path, contents, c.want)
			}
		})
	}
}

// An empty cache file would read in bluetoothd as a device with no
// service records.
func TestWriteTreeWritesNoCacheFileWithoutOne(t *testing.T) {
	root := t.TempDir()
	tree := Tree{address(t, testDevice): files(testInfo, "")}

	if err := WriteTree(root, address(t, testAdapter), tree); err != nil {
		t.Fatalf("WriteTree returned %v, want no error", err)
	}

	path := filepath.Join(root, "04:4A:69:66:92:27", "cache", "7C:66:EF:22:E7:80")
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stat %s returned %v, want it absent", path, err)
	}
}

// An info file holds a link key, so its mode must be the one BlueZ
// uses. The cache file has no key but gets the same modes, because
// BlueZ uses them for its whole tree.
func TestWriteTreeUsesBlueZsModes(t *testing.T) {
	root := t.TempDir()
	tree := Tree{address(t, testDevice): files(testInfo, testCache)}
	if err := WriteTree(root, address(t, testAdapter), tree); err != nil {
		t.Fatalf("WriteTree returned %v, want no error", err)
	}
	cases := []struct {
		path string
		want os.FileMode
	}{
		{path: "04:4A:69:66:92:27", want: 0o700},
		{path: filepath.Join("04:4A:69:66:92:27", "7C:66:EF:22:E7:80"), want: 0o700},
		{path: filepath.Join("04:4A:69:66:92:27", "7C:66:EF:22:E7:80", "info"), want: 0o600},
		{path: filepath.Join("04:4A:69:66:92:27", "cache"), want: 0o700},
		{path: filepath.Join("04:4A:69:66:92:27", "cache", "7C:66:EF:22:E7:80"), want: 0o600},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			info, err := os.Stat(filepath.Join(root, c.path))
			if err != nil {
				t.Fatalf("reading the mode of %s: %v", c.path, err)
			}
			if got := info.Mode().Perm(); got != c.want {
				t.Errorf("%s has mode %04o, want %04o", c.path, got, c.want)
			}
		})
	}
}

// The operator reads a tree on one machine and the init container
// writes it on another, so WriteTree and ReadTree must be inverses.
func TestWriteTreeThenReadTree(t *testing.T) {
	adapter := address(t, testAdapter)
	tree, err := ReadTree(blueZTree(t), adapter)
	if err != nil {
		t.Fatalf("ReadTree returned %v, want no error", err)
	}

	root := t.TempDir()
	if err := WriteTree(root, adapter, tree); err != nil {
		t.Fatalf("WriteTree returned %v, want no error", err)
	}
	again, err := ReadTree(root, adapter)
	if err != nil {
		t.Fatalf("ReadTree returned %v, want no error", err)
	}
	if !equalTrees(tree, again) {
		t.Fatalf("after WriteTree and ReadTree the tree is %v, want %v", again, tree)
	}
}
