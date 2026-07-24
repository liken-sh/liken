package machine

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeKey(t *testing.T) {
	long := strings.Repeat("a", 300)
	cases := map[string]struct {
		modalias string
		want     func(got string) bool
		reason   string
	}{
		"plain stays unchanged": {
			modalias: "pci:v00001234d00005678bc02",
			want:     func(got string) bool { return got == "pci:v00001234d00005678bc02" },
			reason:   "a plain modalias needs no rewrite",
		},
		"colon and plus survive": {
			modalias: "usb:v1234p5678d0100+alt",
			want:     func(got string) bool { return got == "usb:v1234p5678d0100+alt" },
			reason:   "colon and plus are safe in a path segment",
		},
		"unsafe byte gets a hash suffix": {
			modalias: "usb:v1/2 weird",
			want: func(got string) bool {
				return strings.HasPrefix(got, "usb:v1_2_weird-") && len(got) > len("usb:v1_2_weird-")
			},
			reason: "a slash and a space each become an underscore, and the hash makes the key unique",
		},
		"over-length truncates and hashes": {
			modalias: long,
			want:     func(got string) bool { return len(got) == 64+1+12 },
			reason:   "a modalias past 200 bytes truncates to 64 and appends a dash and twelve hex digits",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := safeKey(tc.modalias); !tc.want(got) {
				t.Errorf("safeKey(%q) = %q: %s", tc.modalias, got, tc.reason)
			}
		})
	}
}

func TestSafeKeyDistinguishesUnsafeBytes(t *testing.T) {
	first := safeKey("usb:v1/2")
	second := safeKey("usb:v1 2")
	if first == second {
		t.Errorf("two aliases that differ only in an unsafe byte share the key %q", first)
	}
}

func TestWriteUnclaimedRemovesVanishedDevices(t *testing.T) {
	tree := FactsTree{Dir: t.TempDir()}
	both := []UnclaimedDevice{
		{Modalias: "pci:v1", Bus: "pci"},
		{Modalias: "pci:v2", Bus: "pci"},
	}
	if err := tree.WriteUnclaimed(both); err != nil {
		t.Fatal(err)
	}
	if err := tree.WriteUnclaimed(both[:1]); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(tree.Dir, "hardware", "unclaimed", safeKey("pci:v2"))
	if _, err := os.Stat(gone); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the vanished device's directory still exists: %v", err)
	}
	kept := filepath.Join(tree.Dir, "hardware", "unclaimed", safeKey("pci:v1"))
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("the present device's directory should stay: %v", err)
	}
}

func TestWriteFeaturesRemovesVanishedFeatures(t *testing.T) {
	tree := FactsTree{Dir: t.TempDir()}
	both := []FeatureStatus{
		{Name: "gpu", State: FeatureActive},
		{Name: "sr-iov", State: FeatureActive},
	}
	if err := tree.WriteFeatures(both); err != nil {
		t.Fatal(err)
	}
	if err := tree.WriteFeatures(both[:1]); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(tree.Dir, "features", "sr-iov")
	if _, err := os.Stat(gone); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the retracted feature's directory still exists: %v", err)
	}
}

func TestReconcileLeavesTempFilesAlone(t *testing.T) {
	tree := FactsTree{Dir: t.TempDir()}
	parent := filepath.Join(tree.Dir, "hardware", "unclaimed")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(parent, ".liken-inflight")
	if err := os.WriteFile(temp, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tree.WriteUnclaimed([]UnclaimedDevice{{Modalias: "pci:v1", Bus: "pci"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(temp); err != nil {
		t.Errorf("reconcile removed another write's temp file: %v", err)
	}
}

func TestReadIgnoresUnknownEntries(t *testing.T) {
	tree := FactsTree{Dir: t.TempDir()}
	writeAll(t, tree, sparseFacts())
	if err := os.WriteFile(filepath.Join(tree.Dir, "surprise"), []byte("junk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(tree.Dir, "hardware", "blockDevices")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stray, "README"), []byte("not a device\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tree.Read(); err != nil {
		t.Errorf("an unknown file or directory should not break the read: %v", err)
	}
}

func TestReadMissingRoot(t *testing.T) {
	tree := FactsTree{Dir: filepath.Join(t.TempDir(), "absent")}
	_, err := tree.Read()
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing root should report fs.ErrNotExist, got %v", err)
	}
}

func TestReadReportsUnparseableInteger(t *testing.T) {
	tree := FactsTree{Dir: t.TempDir()}
	writeAll(t, tree, sparseFacts())
	if err := os.MkdirAll(filepath.Join(tree.Dir, "hardware"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree.Dir, "hardware", "cpus"), []byte("three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := tree.Read()
	if err == nil {
		t.Fatal("a non-integer cpus file should be an error")
	}
	if !strings.Contains(err.Error(), filepath.Join("hardware", "cpus")) {
		t.Errorf("the error should name the bad file's path, got %v", err)
	}
}
