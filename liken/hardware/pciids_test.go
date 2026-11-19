package hardware

import (
	"os"
	"path/filepath"
	"testing"
)

func pciIDsFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pci.ids")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing the pci.ids file: %v", err)
	}
	return path
}

const samplePCIIDs = `# PCI ID database
1af4  Red Hat, Inc.
	1041  Virtio 1.0 network device
	1050  Virtio 1.0 GPU
		1af4 1100  QEMU Virtual Machine
8086  Intel Corporation
	10d3  82574L Gigabit Network Connection

# List of known device classes
C 03  Display controller
	00  VGA compatible controller
`

func TestPCIIDsNamesVendorAndDevice(t *testing.T) {
	ids, err := LoadPCIIDs(pciIDsFile(t, samplePCIIDs))
	if err != nil {
		t.Fatalf("loading the pci.ids file: %v", err)
	}
	if got := ids.Name("1af4", "1050"); got != "Red Hat, Inc. Virtio 1.0 GPU" {
		t.Errorf(`Name("1af4", "1050") = %q, want "Red Hat, Inc. Virtio 1.0 GPU"`, got)
	}
	if got := ids.Name("8086", "10d3"); got != "Intel Corporation 82574L Gigabit Network Connection" {
		t.Errorf(`Name("8086", "10d3") = %q, want "Intel Corporation 82574L Gigabit Network Connection"`, got)
	}
}

func TestPCIIDsFallsBackToVendorAlone(t *testing.T) {
	ids, err := LoadPCIIDs(pciIDsFile(t, samplePCIIDs))
	if err != nil {
		t.Fatal(err)
	}
	if got := ids.Name("1af4", "ffff"); got != "Red Hat, Inc. device ffff" {
		t.Errorf(`Name("1af4", "ffff") = %q, want "Red Hat, Inc. device ffff"`, got)
	}
}

func TestPCIIDsUnknownVendorNamesNothing(t *testing.T) {
	ids, err := LoadPCIIDs(pciIDsFile(t, samplePCIIDs))
	if err != nil {
		t.Fatal(err)
	}
	if got := ids.Name("abcd", "1234"); got != "" {
		t.Errorf(`Name("abcd", "1234") = %q, want "" for an unknown vendor`, got)
	}
}

func TestPCIIDsIgnoresTheClassSection(t *testing.T) {
	ids, err := LoadPCIIDs(pciIDsFile(t, samplePCIIDs))
	if err != nil {
		t.Fatal(err)
	}
	if got := ids.Name("c 03", "00"); got != "" {
		t.Errorf(`Name("c 03", "00") = %q, want "" because the class section is skipped`, got)
	}
}

func TestLoadPCIIDsMissingFileIsAnError(t *testing.T) {
	_, err := LoadPCIIDs(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("LoadPCIIDs of a missing file returned no error, want an error")
	}
}
