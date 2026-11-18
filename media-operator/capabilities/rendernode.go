package main

// The claim makes every render node of the machine available in this
// container, and the allocation identifies each one only by the `liken`
// device, which has the GPU's PCI address as an attribute. sysfs lists
// the DRM nodes of a PCI device in the device's own directory, so the
// address finds the node's name in /dev/dri.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A test points sysRoot at a temporary directory.
var (
	sysRoot = "/sys"
	devRoot = "/dev"
)

// renderNodePath returns the path of the render node of the PCI device
// at address, such as /dev/dri/renderD128.
func renderNodePath(address string) (string, error) {
	dir := filepath.Join(sysRoot, "bus", "pci", "devices", address, "drm")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "renderD") {
			return filepath.Join(devRoot, "dri", entry.Name()), nil
		}
	}
	return "", fmt.Errorf("%s has no entry named renderD*", dir)
}
