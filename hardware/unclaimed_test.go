package hardware

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// catalog builds a Catalog from inline table content, the fixture
// for every judgment test: aliases in modules.alias form, shipped
// and builtin as module names.
func catalog(t *testing.T, aliases string, shipped, builtin []string) *Catalog {
	t.Helper()
	table, err := LoadAliasTable(aliasFile(t, aliases))
	if err != nil {
		t.Fatal(err)
	}
	set := func(names []string) map[string]bool {
		s := map[string]bool{}
		for _, n := range names {
			s[strings.ReplaceAll(n, "-", "_")] = true
		}
		return s
	}
	return &Catalog{Aliases: table, Shipped: set(shipped), Builtin: set(builtin)}
}

const stickModalias = "usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00"

const stickAliases = `alias usb:v*p*d*dc*dsc*dp*ic08isc06ip50in* usb_storage
alias usb:v*p*d*dc*dsc*dp*ic08isc06ip*in* uas
`

func TestUnclaimedReportsAnUndrivenDeviceWithShippedCandidates(t *testing.T) {
	c := catalog(t, stickAliases, []string{"usb_storage", "uas"}, nil)
	devices := []Device{{Bus: "usb", Modalias: stickModalias,
		Name: "QEMU QEMU USB HARDDRIVE", Class: "mass-storage"}}

	got := c.Unclaimed(devices)

	if len(got) != 1 {
		t.Fatalf("Unclaimed = %+v, want 1 entry", got)
	}
	u := got[0]
	if u.Modalias != stickModalias || u.Bus != "usb" ||
		u.Name != "QEMU QEMU USB HARDDRIVE" || u.Class != "mass-storage" {
		t.Errorf("entry = %+v", u)
	}
	if len(u.Candidates) != 2 || u.Candidates[0] != "usb_storage" || u.Candidates[1] != "uas" {
		t.Errorf("Candidates = %v, want [usb_storage uas]", u.Candidates)
	}
	if u.Message != "declare usb_storage or uas in spec.modules" {
		t.Errorf("Message = %q", u.Message)
	}
}

func TestUnclaimedNamesTheFixWhenTheImageLacksTheDriver(t *testing.T) {
	c := catalog(t, stickAliases, nil, nil)
	devices := []Device{{Bus: "usb", Modalias: stickModalias}}

	got := c.Unclaimed(devices)

	if len(got) != 1 {
		t.Fatalf("Unclaimed = %+v, want 1 entry", got)
	}
	if got[0].Message != "usb_storage or uas would drive it, but this image carries neither; upgrade to a release that does" {
		t.Errorf("Message = %q", got[0].Message)
	}
}

func TestUnclaimedPrefersShippedCandidatesInTheMessage(t *testing.T) {
	c := catalog(t, stickAliases, []string{"usb_storage"}, nil)
	devices := []Device{{Bus: "usb", Modalias: stickModalias}}

	got := c.Unclaimed(devices)

	if len(got) != 1 {
		t.Fatalf("Unclaimed = %+v, want 1 entry", got)
	}
	if got[0].Message != "declare usb_storage in spec.modules" {
		t.Errorf("Message = %q", got[0].Message)
	}
	if len(got[0].Candidates) != 2 {
		t.Errorf("Candidates = %v, want both candidates reported", got[0].Candidates)
	}
}

func TestUnclaimedSkipsBoundDevices(t *testing.T) {
	c := catalog(t, stickAliases, []string{"usb_storage"}, nil)
	devices := []Device{{Bus: "usb", Modalias: stickModalias, Driver: "usb-storage"}}

	if got := c.Unclaimed(devices); got != nil {
		t.Errorf("Unclaimed = %+v, want nil for a driven device", got)
	}
}

func TestUnclaimedSkipsDevicesNoModuleCouldDrive(t *testing.T) {
	c := catalog(t, stickAliases, []string{"usb_storage"}, nil)
	devices := []Device{{Bus: "pci",
		Modalias: "pci:v00008086d00001237sv00000000sd00000000bc06sc00i00"}}

	if got := c.Unclaimed(devices); got != nil {
		t.Errorf("Unclaimed = %+v, want nil when no alias matches", got)
	}
}

func TestUnclaimedSkipsDevicesWhoseOnlyCandidateIsBuiltin(t *testing.T) {
	c := catalog(t, "alias usb:v*p*d*dc*dsc*dp*ic09isc00ip*in* hub\n",
		nil, []string{"hub"})
	devices := []Device{{Bus: "usb",
		Modalias: "usb:v1D6Bp0002d0515dc09dsc00dp01ic09isc00ip00in00"}}

	if got := c.Unclaimed(devices); got != nil {
		t.Errorf("Unclaimed = %+v, want nil when the driver is resident already", got)
	}
}

func TestUnclaimedSortsByBusThenModalias(t *testing.T) {
	aliases := stickAliases + "alias pci:v00001AF4d00001050sv*sd*bc*sc*i* virtio_gpu\n"
	c := catalog(t, aliases, []string{"usb_storage", "uas", "virtio_gpu"}, nil)
	devices := []Device{
		{Bus: "usb", Modalias: stickModalias},
		{Bus: "pci", Modalias: "pci:v00001AF4d00001050sv00001AF4sd00001100bc03sc80i00"},
	}

	got := c.Unclaimed(devices)

	if len(got) != 2 || got[0].Bus != "pci" || got[1].Bus != "usb" {
		t.Errorf("Unclaimed order = %+v, want pci before usb", got)
	}
}

func TestLoadModuleSetReadsBuiltinLists(t *testing.T) {
	dir := t.TempDir()
	builtin := filepath.Join(dir, "modules.builtin")
	if err := os.WriteFile(builtin, []byte("kernel/drivers/usb/core/usbcore.ko\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resident := LoadModuleSet(builtin)
	if !resident["usbcore"] {
		t.Errorf("builtin = %v, want usbcore present", resident)
	}
}

func TestLoadShippedModulesBelievesFilesNotTheIndex(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "kernel/drivers/usb/storage"), 0o755); err != nil {
		t.Fatal(err)
	}
	aboard := filepath.Join(dir, "kernel/drivers/usb/storage/usb-storage.ko.zst")
	if err := os.WriteFile(aboard, []byte("elf, allegedly"), 0o644); err != nil {
		t.Fatal(err)
	}
	dep := filepath.Join(dir, "modules.dep")
	if err := os.WriteFile(dep, []byte(
		"kernel/drivers/usb/storage/usb-storage.ko.zst:\n"+
			"kernel/drivers/usb/storage/uas.ko.zst:\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	shipped := LoadShippedModules(dir)
	if !shipped["usb_storage"] {
		t.Errorf("shipped = %v, want usb_storage (its file is aboard)", shipped)
	}
	if shipped["uas"] {
		t.Errorf("shipped = %v, want uas absent (indexed but not aboard)", shipped)
	}
}
