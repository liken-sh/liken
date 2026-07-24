package main

// Tests for what the report boot recommends. The recommendation is
// the one part of the boot flow that a test can drive whole: it reads
// a sysfs tree and a module tree, and both can be fabricated. The
// disk-side helpers have their own tests in reportdisks_test.go.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/hardware"
)

// fakeBus points the hardware walk at a sysfs tree of the test's
// making, and restores the real one when the test ends.
func fakeBus(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	old := sysfsRoot
	sysfsRoot = root
	t.Cleanup(func() { sysfsRoot = old })
	return root
}

// addPCIDevice writes the four attributes the walk reads for a PCI
// function: its fingerprint, its class code, and its numeric identity.
// The class code is what decides whether the report will touch the
// device at all: 0x02 is a network controller, 0x01 is storage, and
// 0x03 is a display.
func addPCIDevice(t *testing.T, root, address, class, modalias string) {
	t.Helper()
	dir := filepath.Join(root, "bus", "pci", "devices", address)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSysfs(t, dir, "modalias", modalias+"\n")
	writeSysfs(t, dir, "class", class+"\n")
	writeSysfs(t, dir, "vendor", "0x10ec\n")
	writeSysfs(t, dir, "device", "0x8168\n")
}

// fixtureCatalog builds the smallest catalog that can name a driver:
// an alias table with one line per module. Nothing is built in and
// nothing is shipped, so every match reads as a loadable candidate.
func fixtureCatalog(t *testing.T, aliases map[string]string) (*hardware.Catalog, string) {
	t.Helper()
	base := t.TempDir()
	var table strings.Builder
	for pattern, module := range aliases {
		fmt.Fprintf(&table, "alias %s %s\n", pattern, module)
	}
	writeSysfs(t, base, "modules.alias", table.String())
	catalog, err := hardware.LoadCatalog(base, filepath.Join(base, "pci.ids"))
	if err != nil {
		t.Fatal(err)
	}
	return catalog, base
}

const (
	nicModalias     = "pci:v000010ECd00008168sv00sd00bc02sc00i00"
	displayModalias = "pci:v00001234d00001111sv00sd00bc03sc00i00"
	hbaModalias     = "pci:v00001000d00000097sv00sd00bc01sc07i00"
)

func TestRecommendationsCoverStorageAndNetworkOnly(t *testing.T) {
	// A display controller wants a driver as much as the NIC does, and
	// loading it would switch the framebuffer console the person is
	// reading the report on. The report leaves it alone.
	root := fakeBus(t)
	addPCIDevice(t, root, "0000:00:02.0", "0x030000", displayModalias)
	addPCIDevice(t, root, "0000:00:1f.6", "0x020000", nicModalias)
	addPCIDevice(t, root, "0000:03:00.0", "0x010700", hbaModalias)
	catalog, base := fixtureCatalog(t, map[string]string{
		displayModalias: "bochs",
		nicModalias:     "r8169",
		hbaModalias:     "mpt3sas",
	})

	recs := recommendModules(catalog, base)

	var chains []string
	for _, rec := range recs {
		chains = append(chains, strings.Join(rec.Chain, ","))
	}
	if len(recs) != 2 {
		t.Fatalf("the report must recommend the NIC and the HBA only: %v", chains)
	}
	for _, rec := range recs {
		if rec.Class != "network" && rec.Class != "storage" {
			t.Errorf("a %s device has no place in the report: %+v", rec.Class, rec)
		}
	}
}

func TestRecommendationsAreOnePerFingerprint(t *testing.T) {
	// Two identical NICs are one recommendation with two devices behind
	// it. One chain drives both cards, and a proposal that printed the
	// same evidence twice would read as two different findings.
	root := fakeBus(t)
	addPCIDevice(t, root, "0000:01:00.0", "0x020000", nicModalias)
	addPCIDevice(t, root, "0000:01:00.1", "0x020000", nicModalias)
	catalog, base := fixtureCatalog(t, map[string]string{nicModalias: "r8169"})

	recs := recommendModules(catalog, base)

	if len(recs) != 1 {
		t.Fatalf("two identical cards are one recommendation: %+v", recs)
	}
	if len(recs[0].SysfsDirs) != 2 {
		t.Errorf("the recommendation must keep both devices: %+v", recs[0].SysfsDirs)
	}
}

func TestRecommendationsSkipDevicesWithNoCandidate(t *testing.T) {
	root := fakeBus(t)
	addPCIDevice(t, root, "0000:00:1f.6", "0x020000", nicModalias)
	catalog, base := fixtureCatalog(t, map[string]string{"pci:v0000FFFFd*": "nothing"})

	if recs := recommendModules(catalog, base); len(recs) != 0 {
		t.Errorf("a device no module matches has no recommendation: %+v", recs)
	}
}
