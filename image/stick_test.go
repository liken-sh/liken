package image

// Tests for the install stick, read back the way firmware would:
// find the ESP in the partition table, open the filesystem inside
// it, and check every file the boot path and the installer depend
// on.

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/disks"
	"github.com/liken-sh/liken/machine"
)

// stickFixture builds a release directory (with the boot menu
// artifact the stick requires) and a real two-machine layer, and
// runs Stick over them.
func stickFixture(t *testing.T, consoles []string) (string, *disks.FATVolume) {
	t.Helper()
	releaseDir := releaseFixtureWith(t, map[string]string{
		"vmlinuz":             "kernel bytes",
		"liken.cpio":          "generic image bytes",
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

	// The firmware's path: the partition table names one EFI system
	// partition, and the filesystem lives inside its extent.
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

func TestStickListsEveryMachine(t *testing.T) {
	_, v := stickFixture(t, nil)

	for _, name := range []string{"node-1", "node-2"} {
		entry, err := v.ReadFile("loader/entries/" + name + ".conf")
		if err != nil {
			t.Fatalf("%s has no menu entry: %v", name, err)
		}
		text := string(entry)
		for _, want := range []string{
			"title install as " + name,
			"sort-key " + name,
			"linux /vmlinuz",
			"initrd /liken.cpio",
			"initrd /deployment.cpio",
			"initrd /payload.cpio",
			"options rdinit=/liken liken.machine=" + name + " liken.install",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s's entry is missing %q:\n%s", name, want, text)
			}
		}
		if strings.Contains(text, "console=") {
			t.Errorf("no console= without the flag; hardware defaults to its screen:\n%s", text)
		}
	}
}

func TestStickBakesConsoles(t *testing.T) {
	_, v := stickFixture(t, []string{"ttyS0", "tty0"})
	entry, err := v.ReadFile("loader/entries/node-1.conf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entry), "console=ttyS0 console=tty0") {
		t.Errorf("every -console lands on the options line, in order:\n%s", entry)
	}
}

func TestStickCarriesTheBootFilesAndPayload(t *testing.T) {
	_, v := stickFixture(t, nil)

	if got, err := v.ReadFile("vmlinuz"); err != nil || string(got) != "kernel bytes" {
		t.Errorf("vmlinuz: %v", err)
	}
	if got, err := v.ReadFile("liken.cpio"); err != nil || string(got) != "generic image bytes" {
		t.Errorf("liken.cpio: %v", err)
	}

	layer, err := v.ReadFile(machine.LayerName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := machineNames(layer); err != nil {
		t.Errorf("the stick's layer must be the real archive: %v", err)
	}

	// The payload is the slot layout: everything the document lists,
	// the document itself, and the layer beside its sidecar.
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
		"usr/share/liken/release/liken.cpio",
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

func TestStickRefusesAReleaseWithoutTheMenu(t *testing.T) {
	releaseDir := releaseFixtureWith(t, map[string]string{
		"vmlinuz":    "kernel bytes",
		"liken.cpio": "generic image bytes",
		"liken":      "toolkit bytes",
	})
	layer := filepath.Join(t.TempDir(), machine.LayerName)
	if err := os.WriteFile(layer, mintedLayer(t, "node-1"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Stick(releaseDir, layer, filepath.Join(t.TempDir(), "out.img"), nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "predates install sticks") {
		t.Errorf("a release without systemd-boot must be refused by name: %v", err)
	}
}

func TestStickRefusesATamperedRelease(t *testing.T) {
	releaseDir := releaseFixtureWith(t, map[string]string{
		"vmlinuz":             "kernel bytes",
		"liken.cpio":          "generic image bytes",
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
		"liken.cpio":          "generic image bytes",
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
	// Tiny fixtures: the FAT32 floor dominates, and the image stays
	// close to it rather than ballooning to some fixed guess.
	if info.Size() < 300<<20 || info.Size() > 320<<20 {
		t.Errorf("image is %d MB; expected just past the FAT32 floor for these contents", info.Size()>>20)
	}
}
