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
		t.Fatalf("ReadTree: %v", err)
	}
	// cache/ holds an entry for the paired device and one for a
	// neighbour's device. Only the paired one has a directory of its
	// own, and only that one travels. settings is the adapter's own
	// state. Carrying the neighbour's entry would put their hardware
	// into the API.
	if len(tree) != 1 {
		t.Fatalf("got %d bonds, want 1: %v", len(tree), tree)
	}
	device, ok := tree[address(t, testDevice)]
	if !ok {
		t.Fatalf("the paired device is missing: %v", tree)
	}
	if string(device.Info) != testInfo {
		t.Errorf("info = %q", device.Info)
	}
}

// The cache file holds the SDP records, and a BR/EDR HID device does
// not reconnect without them. bluetoothd logs "Could not parse HID SDP
// record" and the controller drops the connection at once.
func TestReadTreeCarriesTheCacheEntryOfAPairedDevice(t *testing.T) {
	tree, err := ReadTree(blueZTree(t), address(t, testAdapter))
	if err != nil {
		t.Fatalf("ReadTree: %v", err)
	}

	if got := string(tree[address(t, testDevice)].Cache); got != testCache {
		t.Errorf("cache = %q, want %q", got, testCache)
	}
}

// A neighbour's phone has a cache entry and no directory of its own.
// Nothing about it reaches the tree, by either address.
func TestReadTreeLeavesTheCacheEntriesOfUnpairedDevices(t *testing.T) {
	tree, err := ReadTree(blueZTree(t), address(t, testAdapter))
	if err != nil {
		t.Fatalf("ReadTree: %v", err)
	}

	if _, found := tree[address(t, testNeighbour)]; found {
		t.Fatalf("a device this adapter never paired with reached the tree: %v", tree)
	}
	for device, stored := range tree {
		if string(stored.Cache) == testNeighbourCache {
			t.Errorf("%s carries the neighbour's cache entry", device)
		}
	}
}

// bluetoothd writes the cache entry at name resolution, so a device
// paired from an incoming connection can have none yet. That is a
// bond with one file and not a broken read.
func TestReadTreeAcceptsAPairedDeviceWithNoCacheEntry(t *testing.T) {
	root := blueZTree(t)
	if err := os.Remove(filepath.Join(root, testAdapter, "cache", testDevice)); err != nil {
		t.Fatal(err)
	}

	tree, err := ReadTree(root, address(t, testAdapter))
	if err != nil {
		t.Fatalf("ReadTree: %v", err)
	}

	device, ok := tree[address(t, testDevice)]
	if !ok {
		t.Fatalf("the paired device is missing: %v", tree)
	}
	if string(device.Info) != testInfo {
		t.Errorf("info = %q", device.Info)
	}
	if len(device.Cache) != 0 {
		t.Errorf("cache = %q, want none", device.Cache)
	}
}

func TestReadTreeSkipsEntriesThatAreNotBonds(t *testing.T) {
	root := blueZTree(t)
	adapter := filepath.Join(root, testAdapter)

	// A directory whose name is an address but which holds no info
	// file: BlueZ leaves one behind when a pairing does not complete.
	if err := os.MkdirAll(filepath.Join(adapter, "AA:BB:CC:DD:EE:FF"), 0o700); err != nil {
		t.Fatal(err)
	}
	// An empty info file carries no link key.
	if err := os.MkdirAll(filepath.Join(adapter, "AA:BB:CC:DD:EE:00"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adapter, "AA:BB:CC:DD:EE:00", "info"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	tree, err := ReadTree(root, address(t, testAdapter))
	if err != nil {
		t.Fatalf("ReadTree: %v", err)
	}
	if len(tree) != 1 {
		t.Fatalf("got %d bonds, want 1: %v", len(tree), tree)
	}
}

// The info file is the bond. A read that fails on it for any reason
// other than the file's absence means the tree is not readable, and a
// silent skip would write a Secret with this bond missing. The read is
// forced to fail here by replacing the info file with a directory,
// which os.ReadFile refuses with a non-ENOENT error.
func TestReadTreeFailsOnAnUnreadableInfoFile(t *testing.T) {
	root := blueZTree(t)
	info := filepath.Join(root, testAdapter, testDevice, "info")
	if err := os.Remove(info); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(info, 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadTree(root, address(t, testAdapter)); err == nil {
		t.Fatal("an info file that fails to read must fail the whole call")
	}
}

// An adapter that has paired nothing has no directory at all, which
// is the ordinary state of a machine on its first start.
func TestReadTreeWithNoDirectory(t *testing.T) {
	tree, err := ReadTree(t.TempDir(), address(t, testAdapter))
	if err != nil {
		t.Fatalf("ReadTree: %v", err)
	}
	if len(tree) != 0 {
		t.Fatalf("got %v, want an empty tree", tree)
	}
}

func TestWriteTreeWritesWhereBlueZReads(t *testing.T) {
	root := t.TempDir()
	tree := Tree{address(t, testDevice): files(testInfo, testCache)}

	if err := WriteTree(root, address(t, testAdapter), tree); err != nil {
		t.Fatalf("WriteTree: %v", err)
	}

	// The paths are BlueZ's own: uppercase, with colons, the info file
	// below the adapter and the device, and the cache entry in a
	// directory the adapter shares between its devices.
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
				t.Errorf("contents = %q, want %q", contents, c.want)
			}
		})
	}
}

// A device with no stored cache entry gets no cache file. An empty one
// would give bluetoothd a key file with no [ServiceRecords] group,
// which reads as a device whose records are known to be none.
func TestWriteTreeWritesNoCacheFileWithoutOne(t *testing.T) {
	root := t.TempDir()
	tree := Tree{address(t, testDevice): files(testInfo, "")}

	if err := WriteTree(root, address(t, testAdapter), tree); err != nil {
		t.Fatalf("WriteTree: %v", err)
	}

	path := filepath.Join(root, "04:4A:69:66:92:27", "cache", "7C:66:EF:22:E7:80")
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stat %s = %v, want it absent", path, err)
	}
}

// The link key in an info file opens every session the device holds,
// so the modes match what BlueZ writes itself. The cache entry holds
// no key and takes the same modes, because BlueZ writes its whole tree
// this way and a mode BlueZ does not use is a difference to explain.
func TestWriteTreeUsesBlueZsModes(t *testing.T) {
	root := t.TempDir()
	tree := Tree{address(t, testDevice): files(testInfo, testCache)}
	if err := WriteTree(root, address(t, testAdapter), tree); err != nil {
		t.Fatalf("WriteTree: %v", err)
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
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != c.want {
				t.Errorf("mode = %04o, want %04o", got, c.want)
			}
		})
	}
}

// What the operator reads from one machine is what the init container
// writes on the next one, so the two halves have to agree on the whole
// tree.
func TestWriteTreeThenReadTree(t *testing.T) {
	adapter := address(t, testAdapter)
	tree, err := ReadTree(blueZTree(t), adapter)
	if err != nil {
		t.Fatalf("ReadTree: %v", err)
	}

	root := t.TempDir()
	if err := WriteTree(root, adapter, tree); err != nil {
		t.Fatalf("WriteTree: %v", err)
	}
	again, err := ReadTree(root, adapter)
	if err != nil {
		t.Fatalf("ReadTree: %v", err)
	}
	if !tree.Same(again) {
		t.Fatalf("the tree changed on the way through disk: %v, want %v", again, tree)
	}
}
