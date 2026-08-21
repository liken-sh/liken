package bonds

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

// The addresses of the machine this design was measured on: one
// adapter, one paired controller, and one neighbour's device that
// BlueZ detected on the air and holds no link key for.
const (
	testAdapter   = "04:4A:69:66:92:27"
	testDevice    = "7C:66:EF:22:E7:80"
	testNeighbour = "E3:28:E9:23:21:6F"
)

// address parses a literal that the test itself wrote, so a failure
// here is a broken test and not a broken input.
func address(t *testing.T, literal string) Address {
	t.Helper()
	parsed, err := ParseAddress(literal)
	if err != nil {
		t.Fatalf("ParseAddress(%q): %v", literal, err)
	}
	return parsed
}

// blueZTree builds the storage tree BlueZ writes, as a real machine
// has it: one paired device with an info file and an empty attributes
// file, a cache directory holding an entry for the paired device and
// one for a neighbour's, and the adapter's own settings file.
func blueZTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	adapter := filepath.Join(root, testAdapter)

	write := func(path string, contents string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(adapter, testDevice, "info"), testInfo)
	write(filepath.Join(adapter, testDevice, "attributes"), "")
	write(filepath.Join(adapter, "cache", testDevice), testCache)
	write(filepath.Join(adapter, "cache", testNeighbour), testNeighbourCache)
	write(filepath.Join(adapter, "settings"), "")
	return root
}

// testInfo is one paired device's info file, in BlueZ's own shape.
// The link key makes this file worth a Secret.
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

// testCache is the same device's cache file, cut down from the one on
// the lab machine. The HID SDP record under [ServiceRecords] is why
// this file travels: the input profile parses it when the controller
// connects, and bluetoothd runs no new discovery for a device it
// already holds a bond for.
const testCache = `[General]
Name=DualSense Wireless Controller

[ServiceRecords]
0x00010000=35760900000A0001000009000135031124
`

// testNeighbourCache is a cache entry for a device this adapter has
// never paired with. It has a name and no key, and it must not
// reach the API.
const testNeighbourCache = `[General]
Name=Somebody's Phone
`

// files builds one device's stored files out of the two strings.
func files(info, cache string) Files {
	return Files{Info: []byte(info), Cache: []byte(cache)}
}

// equalTrees reports whether two trees hold the same devices with the
// same files. It compares each device with Files.Equal, so a round-trip
// test measures the same equality the operator uses.
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
				t.Errorf("equalTrees = %v, want %v", got, c.want)
			}
		})
	}
}
