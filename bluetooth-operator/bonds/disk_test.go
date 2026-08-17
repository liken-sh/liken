package bonds

import (
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
	// neighbour's device, and neither is a bond. settings is the
	// adapter's own state. Reading any of them would put a neighbour's
	// hardware into the API.
	if len(tree) != 1 {
		t.Fatalf("got %d bonds, want 1: %v", len(tree), tree)
	}
	info, ok := tree[address(t, testDevice)]
	if !ok {
		t.Fatalf("the paired device is missing: %v", tree)
	}
	if string(info) != testInfo {
		t.Errorf("info = %q", info)
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
	tree := Tree{address(t, testDevice): []byte(testInfo)}

	if err := WriteTree(root, address(t, testAdapter), tree); err != nil {
		t.Fatalf("WriteTree: %v", err)
	}

	// The path is BlueZ's own: uppercase, with colons, and the info
	// file below the adapter and the device.
	path := filepath.Join(root, "04:4A:69:66:92:27", "7C:66:EF:22:E7:80", "info")
	info, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if string(info) != testInfo {
		t.Errorf("info = %q", info)
	}
}

// The link key in an info file opens every session the device holds,
// so the modes match what BlueZ writes itself.
func TestWriteTreeUsesBlueZsModes(t *testing.T) {
	root := t.TempDir()
	if err := WriteTree(root, address(t, testAdapter), Tree{address(t, testDevice): []byte(testInfo)}); err != nil {
		t.Fatalf("WriteTree: %v", err)
	}
	cases := []struct {
		path string
		want os.FileMode
	}{
		{path: "04:4A:69:66:92:27", want: 0o700},
		{path: filepath.Join("04:4A:69:66:92:27", "7C:66:EF:22:E7:80"), want: 0o700},
		{path: filepath.Join("04:4A:69:66:92:27", "7C:66:EF:22:E7:80", "info"), want: 0o600},
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
