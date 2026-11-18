package bonds

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

// An adapter, a paired controller, and a nearby device that BlueZ saw
// advertising and has no link key for.
const (
	testAdapter   = "04:4A:69:66:92:27"
	testDevice    = "7C:66:EF:22:E7:80"
	testNeighbour = "E3:28:E9:23:21:6F"
)

func address(t *testing.T, literal string) Address {
	t.Helper()
	parsed, err := ParseAddress(literal)
	if err != nil {
		t.Fatalf("parsing %q as an address: %v", literal, err)
	}
	return parsed
}

// blueZTree writes one adapter's storage tree in the layout that BlueZ
// writes, with files that are not bonds beside the paired device's.
func blueZTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	adapter := filepath.Join(root, testAdapter)

	write := func(path string, contents string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("creating the directory of %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}
	write(filepath.Join(adapter, testDevice, "info"), testInfo)
	write(filepath.Join(adapter, testDevice, "attributes"), "")
	write(filepath.Join(adapter, "cache", testDevice), testCache)
	write(filepath.Join(adapter, "cache", testNeighbour), testNeighbourCache)
	write(filepath.Join(adapter, "settings"), "")
	return root
}

// testInfo is the info file of one paired device. The link key is what
// makes it a bond.
const testInfo = `[LinkKey]
Key=0123456789ABCDEF0123456789ABCDEF
Type=4
PINLength=0

[General]
Name=DualSense Wireless Controller
Class=0x002508
SupportedTechnologies=BR/EDR;
Trusted=true
Blocked=false
Services=00001124-0000-1000-8000-00805f9b34fb;
`

// testCache is the cache file of the same device, with its HID SDP
// record.
const testCache = `[General]
Name=DualSense Wireless Controller

[ServiceRecords]
0x00010000=35760900000A0001000009000135031124
`

// testNeighbourCache is a cache entry for a device that was never
// paired. It has a name and no key, so it must not be stored.
const testNeighbourCache = `[General]
Name=Somebody's Phone
`

func files(info, cache string) Files {
	return Files{Info: []byte(info), Cache: []byte(cache)}
}

// equalTrees compares two trees with Files.Equal, as the operator does.
func equalTrees(a, b Tree) bool {
	return maps.EqualFunc(a, b, Files.Equal)
}

func TestEqualTrees(t *testing.T) {
	one, two := address(t, testDevice), address(t, testNeighbour)
	cases := []struct {
		name string
		a, b Tree
		want bool
	}{
		{name: "two empty trees", a: Tree{}, b: Tree{}, want: true},
		{name: "an empty tree and a nil one", a: Tree{}, b: nil, want: true},
		{
			name: "the same device with the same files",
			a:    Tree{one: files(testInfo, testCache)},
			b:    Tree{one: files(testInfo, testCache)},
			want: true,
		},
		{
			name: "the same device with a rewritten info",
			a:    Tree{one: files(testInfo, testCache)},
			b:    Tree{one: files("[LinkKey]\nKey=FF\n", testCache)},
			want: false,
		},
		{
			name: "the same device with a rewritten cache",
			a:    Tree{one: files(testInfo, testCache)},
			b:    Tree{one: files(testInfo, "[General]\nName=Renamed\n")},
			want: false,
		},
		{
			name: "a cache file that arrived after the pairing",
			a:    Tree{one: files(testInfo, "")},
			b:    Tree{one: files(testInfo, testCache)},
			want: false,
		},
		{
			name: "a device that was paired since",
			a:    Tree{one: files(testInfo, testCache)},
			b:    Tree{one: files(testInfo, testCache), two: files(testInfo, testCache)},
			want: false,
		},
		{
			name: "a device that was unpaired",
			a:    Tree{one: files(testInfo, testCache)},
			b:    Tree{},
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := equalTrees(c.a, c.b); got != c.want {
				t.Errorf("equalTrees(a, b) = %v, want %v", got, c.want)
			}
		})
	}
}
