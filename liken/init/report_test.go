package main

// These tests of the report's driver recommendations read a fabricated
// sysfs tree and module tree.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/hardware"
)

// fakeSysfs points sysfsRoot at an empty temporary directory for the
// test and returns it.
func fakeSysfs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	old := sysfsRoot
	sysfsRoot = root
	t.Cleanup(func() { sysfsRoot = old })
	return root
}

// addPCIDevice writes the sysfs attributes that the hardware walk reads
// for a PCI function. The class decides whether the report loads a
// driver: 0x01 is storage, 0x02 is network, and 0x03 is display.
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

// fixtureCatalog builds a catalog from a modules.alias file with one
// line for each entry of aliases. No module is built in, so every match
// is a loadable candidate.
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
	// The display controller has a driver too, but loading it would
	// switch the framebuffer console that shows the report.
	root := fakeSysfs(t)
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
		t.Fatalf("the recommendations are %v, want the NIC and the HBA only", chains)
	}
	for _, rec := range recs {
		if rec.Class != "network" && rec.Class != "storage" {
			t.Errorf("the recommendation is %+v, want class network or storage, not %s", rec, rec.Class)
		}
	}
}

func TestClaimableCoversWhatTheReportWillNotLoad(t *testing.T) {
	root := fakeSysfs(t)
	addPCIDevice(t, root, "0000:00:02.0", "0x030000", displayModalias)
	addPCIDevice(t, root, "0000:00:1f.6", "0x020000", nicModalias)
	addPCIDevice(t, root, "0000:03:00.0", "0x010700", hbaModalias)
	catalog, base := fixtureCatalog(t, map[string]string{
		displayModalias: "bochs",
		nicModalias:     "r8169",
		hbaModalias:     "mpt3sas",
	})

	recs := recommendClaimable(catalog, base)

	if len(recs) != 1 || recs[0].Class != "display" {
		t.Fatalf("the claimable list is %+v, want the display device alone", recs)
	}
	if !slices.Equal(recs[0].Chain, []string{"bochs"}) {
		t.Errorf("the chain is %v, want [bochs], the driver that would bind the device", recs[0].Chain)
	}
}

func TestClaimableSkipsClassesWithNoDeviceForAPod(t *testing.T) {
	// A driver for a bridge gives a pod no device to claim.
	for _, class := range []string{"bridge", "memory", "system", ""} {
		if claimableClass(class) {
			t.Errorf("claimableClass(%q) is true, want false because a pod cannot claim it", class)
		}
	}
	for _, class := range []string{"display", "multimedia", "vendor-specific", "hid"} {
		if !claimableClass(class) {
			t.Errorf("claimableClass(%q) is false, want true", class)
		}
	}
}

func TestRecommendationsAreOnePerModalias(t *testing.T) {
	// One driver chain serves both identical cards, and listing it twice
	// would look like two different devices.
	root := fakeSysfs(t)
	addPCIDevice(t, root, "0000:01:00.0", "0x020000", nicModalias)
	addPCIDevice(t, root, "0000:01:00.1", "0x020000", nicModalias)
	catalog, base := fixtureCatalog(t, map[string]string{nicModalias: "r8169"})

	recs := recommendModules(catalog, base)

	if len(recs) != 1 {
		t.Fatalf("two identical cards give %+v, want one recommendation", recs)
	}
	if len(recs[0].SysfsDirs) != 2 {
		t.Errorf("the recommendation has the devices %v, want both cards", recs[0].SysfsDirs)
	}
}

func TestRecommendationsSkipDevicesWithNoCandidate(t *testing.T) {
	root := fakeSysfs(t)
	addPCIDevice(t, root, "0000:00:1f.6", "0x020000", nicModalias)
	catalog, base := fixtureCatalog(t, map[string]string{"pci:v0000FFFFd*": "nothing"})

	if recs := recommendModules(catalog, base); len(recs) != 0 {
		t.Errorf("a device that no module matches has the recommendations %+v, want none", recs)
	}
}
