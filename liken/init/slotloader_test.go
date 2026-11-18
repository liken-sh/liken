package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// slotWithMenu returns a temporary directory with the files of a
// mounted slot: systemd-boot, the kernel, and the archives.
func slotWithMenu(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, contents := range map[string]string{
		bootMenuName:      "the menu program",
		"vmlinuz":         "the kernel",
		"microcode.cpio":  "the microcode",
		"boot.cpio":       "the early boot",
		"deployment.cpio": "the layer",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
			t.Fatalf("writing %s to the slot: %v", name, err)
		}
	}
	return dir
}

func TestWriteSlotLoader(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken\n")
	mount := slotWithMenu(t)

	if err := writeSlotLoader(mount, "A", "node-1"); err != nil {
		t.Fatalf("writing the loader to slot A: %v", err)
	}

	loader, err := os.ReadFile(filepath.Join(mount, "EFI", "BOOT", "BOOTX64.EFI"))
	if err != nil {
		t.Fatalf("reading the loader at the default path %s: %v", defaultLoaderPath(mount), err)
	}
	if string(loader) != "the menu program" {
		t.Errorf("the loader is %q, want a copy of the slot's menu program", loader)
	}
	conf, err := os.ReadFile(filepath.Join(mount, "loader", "loader.conf"))
	if err != nil {
		t.Fatalf("reading loader.conf: %v", err)
	}
	if !strings.Contains(string(conf), "timeout 0") {
		t.Errorf("loader.conf is %q, want timeout 0 because nobody is at the console to choose", conf)
	}
	entry, err := os.ReadFile(filepath.Join(mount, "loader", "entries", "liken-A.conf"))
	if err != nil {
		t.Fatalf("reading the entry of slot A: %v", err)
	}
	text := string(entry)
	for _, want := range []string{
		"title    liken slot A\n",
		"linux    /vmlinuz\n",
		"initrd   /microcode.cpio\n",
		"initrd   /boot.cpio\n",
		"initrd   /deployment.cpio\n",
		"options  console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=A panic=10\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the entry is\n%s\nwant it to contain %q", text, want)
		}
	}
	if strings.Contains(text, "initrd=") {
		t.Error("the options line names initrd= parameters, want none because the initrd lines load the archives")
	}
}

func TestSlotLoaderEntryListsOnlyTheArchivesTheSlotHas(t *testing.T) {
	fakeCmdline(t, "console=ttyS0\n")
	mount := slotWithMenu(t)
	if err := os.Remove(filepath.Join(mount, "microcode.cpio")); err != nil {
		t.Fatalf("removing the microcode archive: %v", err)
	}

	text := string(slotLoaderEntryText(mount, "B", "node-1"))
	if strings.Contains(text, "microcode.cpio") {
		t.Errorf("the entry is\n%s\nwant no microcode.cpio because the slot has none", text)
	}
	if !strings.Contains(text, "initrd   /boot.cpio\n") {
		t.Errorf("the entry is\n%s\nwant it to name boot.cpio", text)
	}
}

func TestWriteSlotLoaderRefusesASlotWithNoMenuProgram(t *testing.T) {
	fakeCmdline(t, "console=ttyS0\n")
	mount := t.TempDir()

	if err := writeSlotLoader(mount, "A", "node-1"); err == nil {
		t.Fatal("writeSlotLoader returned no error for a slot with no menu program, want one")
	}
	if _, err := os.Stat(filepath.Join(mount, "EFI")); err == nil {
		t.Error("writeSlotLoader wrote EFI files and returned an error, want no files written")
	}
}

func TestWriteSlotLoaderWritesNothingTwice(t *testing.T) {
	fakeCmdline(t, "console=ttyS0\n")
	mount := slotWithMenu(t)
	if err := writeSlotLoader(mount, "A", "node-1"); err != nil {
		t.Fatalf("writing the loader to slot A: %v", err)
	}
	loader := defaultLoaderPath(mount)
	before, err := os.Stat(loader)
	if err != nil {
		t.Fatalf("reading the loader file: %v", err)
	}

	if err := writeSlotLoader(mount, "A", "node-1"); err != nil {
		t.Fatalf("writing the loader to slot A: %v", err)
	}

	after, err := os.Stat(loader)
	if err != nil {
		t.Fatalf("reading the loader file: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("the loader file was written again, want no write when its contents match")
	}
}

func TestRemoveSlotLoader(t *testing.T) {
	fakeCmdline(t, "console=ttyS0\n")
	mount := slotWithMenu(t)
	if err := writeSlotLoader(mount, "A", "node-1"); err != nil {
		t.Fatalf("writing the loader to slot A: %v", err)
	}

	removeSlotLoader(mount)

	if _, err := os.Stat(defaultLoaderPath(mount)); !os.IsNotExist(err) {
		t.Error("the loader program is still on the slot, want it removed")
	}
	if _, err := os.Stat(filepath.Join(mount, "loader", "entries", "liken-A.conf")); err != nil {
		t.Error("the entry is gone, want removeSlotLoader to leave it")
	}
}

func TestOtherSlot(t *testing.T) {
	if otherSlot("A") != "B" || otherSlot("B") != "A" {
		t.Errorf("otherSlot(\"A\") is %q and otherSlot(\"B\") is %q, want B and A", otherSlot("A"), otherSlot("B"))
	}
}

func TestWriteSlotLoaderRepairsAnEmptyEntry(t *testing.T) {
	fakeCmdline(t, "console=ttyS0\n")
	mount := slotWithMenu(t)
	if err := writeSlotLoader(mount, "A", "node-1"); err != nil {
		t.Fatalf("writing the loader to slot A: %v", err)
	}
	entry := filepath.Join(mount, "loader", "entries", "liken-A.conf")
	if err := os.WriteFile(entry, nil, 0o644); err != nil {
		t.Fatalf("emptying the entry: %v", err)
	}

	if err := writeSlotLoader(mount, "A", "node-1"); err != nil {
		t.Fatalf("writing the loader to slot A: %v", err)
	}

	body, err := os.ReadFile(entry)
	if err != nil {
		t.Fatalf("reading the entry: %v", err)
	}
	if !strings.Contains(string(body), "linux    /vmlinuz\n") {
		t.Errorf("the entry is %q, want it written again with its contents", body)
	}
}
