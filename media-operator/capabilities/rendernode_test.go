package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSysfs makes a sysfs tree with one PCI device whose drm directory
// lists the given nodes, and points the lookup at it.
func fakeSysfs(t *testing.T, address string, nodes ...string) {
	t.Helper()
	root := t.TempDir()
	drm := filepath.Join(root, "bus", "pci", "devices", address, "drm")
	for _, node := range nodes {
		if err := os.MkdirAll(filepath.Join(drm, node), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	saved := sysRoot
	sysRoot = root
	t.Cleanup(func() { sysRoot = saved })
}

func TestTheRenderNodeOfAGPUComesFromItsAddress(t *testing.T) {
	fakeSysfs(t, "0000:00:02.0", "card1", "renderD128")
	got, err := renderNodePath("0000:00:02.0")
	if err != nil || got != "/dev/dri/renderD128" {
		t.Errorf("path = %q, err = %v", got, err)
	}
}

func TestAGPUWithNoRenderNodeFails(t *testing.T) {
	fakeSysfs(t, "0000:00:02.0", "card1")
	_, err := renderNodePath("0000:00:02.0")
	if err == nil || !strings.Contains(err.Error(), "lists no render node") {
		t.Errorf("err = %v", err)
	}
}
