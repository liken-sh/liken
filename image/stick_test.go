package image

// Tests for the install stick, read back the way firmware would:
// find the ESP in the partition table, open the filesystem inside
// it, and check every file that the boot path and the installer
// depend on.

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/disks"
	"github.com/liken-sh/liken/machine"
)

// stickFixture builds a release directory (with the boot menu
// artifact that the stick requires) and a real two-machine layer,
// and runs Stick over them.
func stickFixture(t *testing.T, consoles []string) (string, *disks.FATVolume) {
	t.Helper()
	releaseDir := releaseFixtureWith(t, map[string]string{
		"vmlinuz":             "kernel bytes",
		"liken.sqfs":          "system image bytes",
		"boot.cpio":           "boot archive bytes",
		"microcode.cpio":      "early microcode bytes",
		"liken":               "toolkit bytes",
		"systemd-bootx64.efi": "boot menu program bytes",
	})
	layer := filepath.Join(t.TempDir(), machine.LayerName)
	if err := os.WriteFile(layer, mintedLayer(t, "node-1", "node-2"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "stick.img")
	if err := Stick(releaseDir, layer, out, consoles, io.Discard); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}

	// This is the firmware's path: the partition table names one EFI
	// system partition, and the filesystem lives inside its extent.
	table, err := disks.ReadGPT(f, uint64(info.Size())/disks.SectorSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(table.Entries) != 1 {
		t.Fatalf("the stick carries one partition, got %d", len(table.Entries))
	}
	esp := table.Entries[0]
	if esp.TypeGUID != disks.EFISystemPartition {
		t.Error("the partition must be typed as an EFI system partition or firmware won't look at it")
	}
	if esp.Name != "liken:install" {
		t.Errorf("partition name: %q", esp.Name)
	}
	section := disks.NewSection(f, int64(esp.FirstLBA)*disks.SectorSize,
		int64(esp.LastLBA-esp.FirstLBA+1)*disks.SectorSize)
	volume, err := disks.OpenFATVolume(section)
	if err != nil {
		t.Fatal(err)
	}
	return out, volume
}

func TestStickBootsTheMenu(t *testing.T) {
	_, v := stickFixture(t, nil)

	boot, err := v.ReadFile("EFI/BOOT/BOOTX64.EFI")
	if err != nil || string(boot) != "boot menu program bytes" {
		t.Errorf("BOOTX64.EFI must be the release's systemd-boot, byte for byte: %v", err)
	}
	conf, err := v.ReadFile("loader/loader.conf")
	if err != nil || !strings.Contains(string(conf), "timeout menu-force") {
		t.Errorf("the menu must wait for a person: %q, %v", conf, err)
	}
}

func TestStickGivesEachMachineInstallAndReinstall(t *testing.T) {
	_, v := stickFixture(t, nil)

	for _, name := range []string{"node-1", "node-2"} {
		install, err := v.ReadFile("loader/entries/" + name + "-install.conf")
		if err != nil {
			t.Fatalf("%s has no install entry: %v", name, err)
		}
		for _, want := range []string{
			"title install as " + name,
			"sort-key " + name + "-install",
			"linux /vmlinuz",
			"initrd /microcode.cpio",
			"initrd /boot.cpio",
			"initrd /deployment.cpio",
			"initrd /payload.cpio",
			"options rdinit=/liken liken.machine=" + name + " liken.install",
		} {
			if !strings.Contains(string(install), want) {
				t.Errorf("%s's install entry is missing %q:\n%s", name, want, install)
			}
		}

		reinstall, err := v.ReadFile("loader/entries/" + name + "-reinstall.conf")
		if err != nil {
			t.Fatalf("%s has no reinstall entry: %v", name, err)
		}
		for _, want := range []string{
			"title wipe and reinstall as " + name,
			"sort-key " + name + "-reinstall",
			"options rdinit=/liken liken.machine=" + name + " liken.reinstall",
		} {
			if !strings.Contains(string(reinstall), want) {
				t.Errorf("%s's reinstall entry is missing %q:\n%s", name, want, reinstall)
			}
		}
		// The reinstall entry differs from install in exactly one word:
		// liken.reinstall in place of liken.install.
		if strings.Contains(string(reinstall), "liken.install") {
			t.Errorf("%s's reinstall entry must not carry liken.install:\n%s", name, reinstall)
		}

		if strings.Contains(string(install), "console=") || strings.Contains(string(reinstall), "console=") {
			t.Errorf("no console= without the flag; hardware defaults to its screen")
		}
	}
}

func TestStickCarriesTheHardwareReportEntry(t *testing.T) {
	_, v := stickFixture(t, nil)

	entry, err := v.ReadFile("loader/entries/hardware-report.conf")
	if err != nil {
		t.Fatalf("the stick has no hardware report entry: %v", err)
	}
	text := string(entry)
	for _, want := range []string{
		"title liken hardware report",
		"initrd /payload.cpio",
		"options rdinit=/liken liken.report",
		"hardware-report.yaml",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the report entry is missing %q:\n%s", want, text)
		}
	}
	// The report describes hardware, not a machine, so it carries no
	// identity.
	if strings.Contains(text, "liken.machine=") {
		t.Errorf("the report entry must carry no liken.machine=:\n%s", text)
	}
	// No sort-key: an entry without one sorts after every machine entry,
	// which all carry sort-keys.
	if strings.Contains(text, "sort-key") {
		t.Errorf("the report entry must have no sort-key so it sorts last:\n%s", text)
	}
}

func TestStickSortKeysKeepEachMachinePairAdjacentAndOrdered(t *testing.T) {
	_, v := stickFixture(t, nil)
	// The install key sorts before the reinstall key within a machine,
	// and both sort before the next machine's keys, so the menu reads
	// install/reinstall for node-1, then install/reinstall for node-2.
	keys := []string{
		sortKey(t, v, "node-1-install"),
		sortKey(t, v, "node-1-reinstall"),
		sortKey(t, v, "node-2-install"),
		sortKey(t, v, "node-2-reinstall"),
	}
	if !slices.IsSorted(keys) {
		t.Errorf("the sort-keys must place the pairs in menu order: %v", keys)
	}
}

// sortKey reads one entry's sort-key line back from the stick.
func sortKey(t *testing.T, v *disks.FATVolume, entry string) string {
	t.Helper()
	raw, err := v.ReadFile("loader/entries/" + entry + ".conf")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		if key, ok := strings.CutPrefix(line, "sort-key "); ok {
			return key
		}
	}
	t.Fatalf("%s has no sort-key line:\n%s", entry, raw)
	return ""
}

func TestStickBakesConsoles(t *testing.T) {
	_, v := stickFixture(t, []string{"ttyS0", "tty0"})
	// Consoles land on every entry: the machine's install and reinstall,
	// and the stick-wide report.
	for _, entry := range []string{"node-1-install", "node-1-reinstall", "hardware-report"} {
		raw, err := v.ReadFile("loader/entries/" + entry + ".conf")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "console=ttyS0 console=tty0") {
			t.Errorf("%s: every -console lands on the options line, in order:\n%s", entry, raw)
		}
	}
}

func TestStickCarriesTheBootFilesAndPayload(t *testing.T) {
	_, v := stickFixture(t, nil)

	if got, err := v.ReadFile("vmlinuz"); err != nil || string(got) != "kernel bytes" {
		t.Errorf("vmlinuz: %v", err)
	}
	if got, err := v.ReadFile("boot.cpio"); err != nil || string(got) != "boot archive bytes" {
		t.Errorf("boot.cpio: %v", err)
	}
	if got, err := v.ReadFile("microcode.cpio"); err != nil || string(got) != "early microcode bytes" {
		t.Errorf("microcode.cpio: %v", err)
	}

	layer, err := v.ReadFile(machine.LayerName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := machineNames(layer); err != nil {
		t.Errorf("the stick's layer must be the real archive: %v", err)
	}

	// The payload is the slot layout: everything the document lists,
	// the document itself, and the layer beside its sidecar file.
	payload, err := v.ReadFile("payload.cpio")
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err := readCPIO(payload)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, e := range entries {
		found[e.name] = true
	}
	for _, want := range []string{
		"usr/share/liken/release/release.yaml",
		"usr/share/liken/release/vmlinuz",
		"usr/share/liken/release/liken.sqfs",
		"usr/share/liken/release/boot.cpio",
		"usr/share/liken/release/liken",
		"usr/share/liken/release/systemd-bootx64.efi",
		"usr/share/liken/release/" + machine.LayerName,
		"usr/share/liken/release/" + machine.LayerSidecarName,
	} {
		if !found[want] {
			t.Errorf("the payload is missing %s", want)
		}
	}
}

func TestStickRefusesAnIncompleteRelease(t *testing.T) {
	// A release from before install sticks (no menu program) or from
	// before early microcode (no microcode.cpio) must be refused by
	// the name of what it lacks.
	for _, missing := range []string{"systemd-bootx64.efi", "microcode.cpio"} {
		files := map[string]string{
			"vmlinuz":             "kernel bytes",
			"liken.sqfs":          "system image bytes",
			"boot.cpio":           "boot archive bytes",
			"microcode.cpio":      "early microcode bytes",
			"liken":               "toolkit bytes",
			"systemd-bootx64.efi": "boot menu program bytes",
		}
		delete(files, missing)
		releaseDir := releaseFixtureWith(t, files)
		layer := filepath.Join(t.TempDir(), machine.LayerName)
		if err := os.WriteFile(layer, mintedLayer(t, "node-1"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := Stick(releaseDir, layer, filepath.Join(t.TempDir(), "out.img"), nil, io.Discard)
		if err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("a release without %s must be refused by name: %v", missing, err)
		}
	}
}

func TestStickRefusesATamperedRelease(t *testing.T) {
	releaseDir := releaseFixtureWith(t, map[string]string{
		"vmlinuz":             "kernel bytes",
		"liken.sqfs":          "system image bytes",
		"boot.cpio":           "boot archive bytes",
		"microcode.cpio":      "early microcode bytes",
		"liken":               "toolkit bytes",
		"systemd-bootx64.efi": "boot menu program bytes",
	})
	if err := os.WriteFile(filepath.Join(releaseDir, "vmlinuz"), []byte("not the kernel"), 0o644); err != nil {
		t.Fatal(err)
	}
	layer := filepath.Join(t.TempDir(), machine.LayerName)
	if err := os.WriteFile(layer, mintedLayer(t, "node-1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Stick(releaseDir, layer, filepath.Join(t.TempDir(), "out.img"), nil, io.Discard); err == nil {
		t.Error("an artifact that fails its document must not be packed onto a stick")
	}
}

func TestStickRefusesBadInputs(t *testing.T) {
	releaseDir := releaseFixtureWith(t, map[string]string{
		"vmlinuz":             "kernel bytes",
		"liken.sqfs":          "system image bytes",
		"boot.cpio":           "boot archive bytes",
		"microcode.cpio":      "early microcode bytes",
		"liken":               "toolkit bytes",
		"systemd-bootx64.efi": "boot menu program bytes",
	})
	goodLayer := filepath.Join(t.TempDir(), machine.LayerName)
	if err := os.WriteFile(goodLayer, mintedLayer(t, "node-1"), 0o644); err != nil {
		t.Fatal(err)
	}
	emptyLayer := filepath.Join(t.TempDir(), machine.LayerName)
	if err := os.WriteFile(emptyLayer, mintedLayer(t), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		releaseDir string
		layer      string
		out        string
	}{
		{"missing layer", releaseDir, filepath.Join(t.TempDir(), "absent"), filepath.Join(t.TempDir(), "out.img")},
		{"layer with no machines", releaseDir, emptyLayer, filepath.Join(t.TempDir(), "out.img")},
		{"missing release", t.TempDir(), goodLayer, filepath.Join(t.TempDir(), "out.img")},
		{"unwritable output", releaseDir, goodLayer, filepath.Join(t.TempDir(), "no-such-dir", "out.img")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Stick(c.releaseDir, c.layer, c.out, nil, io.Discard); err == nil {
				t.Error("a stick built from broken inputs must be refused")
			}
		})
	}
}

func TestStickSizesToItsContents(t *testing.T) {
	out, _ := stickFixture(t, nil)
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	// The fixtures are tiny, so the FAT32 floor sets the size, and the
	// image stays close to it instead of growing to some fixed guess.
	if info.Size() < 300<<20 || info.Size() > 320<<20 {
		t.Errorf("image is %d MB; expected just past the FAT32 floor for these contents", info.Size()>>20)
	}
}
