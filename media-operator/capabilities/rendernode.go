package main

// The render node of one GPU, found from the GPU's PCI address.
//
// The claim delivers every render node of the node into this
// container, and the allocation names each one by the device that
// `liken` published for it. That device states the GPU's PCI address,
// and sysfs lists the DRM nodes of a PCI device under its own
// directory. So the address leads to the node's name, and the name to
// the path in /dev/dri.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// sysRoot and devRoot are variables so a test can point them at a
// directory of its own.
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
	return "", fmt.Errorf("%s lists no render node", dir)
}
