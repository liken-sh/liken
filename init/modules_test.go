package main

// Tests for the module index parsing: the file formats depmod leaves
// behind. Actually loading modules into a kernel is QEMU territory.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadModuleListSkipsCommentsAndBlanks(t *testing.T) {
	path := writeFile(t, "modules.list", "# storage\nvirtio_blk\n\n  ext4  \n")
	names, err := readModuleList(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"virtio_blk", "ext4"}) {
		t.Errorf("got %v", names)
	}
}

func TestReadModuleListToleratesAMissingFile(t *testing.T) {
	names, err := readModuleList(filepath.Join(t.TempDir(), "absent"))
	if names != nil || err != nil {
		t.Errorf("a missing list means nothing to load: %v, %v", names, err)
	}
}

func TestReadModulesDepParsesTheIndex(t *testing.T) {
	path := writeFile(t, "modules.dep",
		"kernel/fs/overlayfs/overlay.ko.zst: kernel/a.ko.zst kernel/b.ko.zst\n"+
			"kernel/drivers/block/virtio-blk.ko.zst:\n"+
			"not an entry\n")
	deps, err := readModulesDep(path)
	if err != nil {
		t.Fatal(err)
	}
	overlay := deps["overlay"]
	if !slices.Equal(overlay, []string{"kernel/fs/overlayfs/overlay.ko.zst", "kernel/a.ko.zst", "kernel/b.ko.zst"}) {
		t.Errorf("got %v", overlay)
	}
	// Module names use "_" and "-" interchangeably; the index keys
	// normalize to "_" so lookups can too.
	if _, ok := deps["virtio_blk"]; !ok {
		t.Errorf("dashes normalize to underscores: %v", deps)
	}
}

func TestModuleNameStripsExtensionsAndNormalizes(t *testing.T) {
	if got := moduleName("kernel/drivers/block/virtio-blk.ko.zst"); got != "virtio_blk" {
		t.Errorf("got %q", got)
	}
	if got := moduleName("kernel/fs/ext4.ko"); got != "ext4" {
		t.Errorf("got %q", got)
	}
}

func TestKernelReleaseAsksTheKernel(t *testing.T) {
	if release := kernelRelease(); release == "" {
		t.Error("uname always has a release string")
	}
}
