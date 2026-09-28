package main

// Tests for the default-path loader on a slot: the program a firmware
// with no boot variables finds by itself.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// slotWithMenu builds a directory that stands in for a mounted system
// slot: the boot menu program that every release carries, and the
// kernel and archives beside it.
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
			t.Fatal(err)
		}
	}
	return dir
}

func TestWriteSlotLoader(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken\n")
	mount := slotWithMenu(t)

	if err := writeSlotLoader(mount, "A", "node-1"); err != nil {
		t.Fatal(err)
	}

	loader, err := os.ReadFile(filepath.Join(mount, "EFI", "BOOT", "BOOTX64.EFI"))
	if err != nil {
		t.Fatalf("a firmware at its defaults looks for this one path: %v", err)
	}
	if string(loader) != "the menu program" {
		t.Errorf("the loader is a copy of the slot's own menu program: %q", loader)
	}
	conf, err := os.ReadFile(filepath.Join(mount, "loader", "loader.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(conf), "timeout 0") {
		t.Errorf("nobody is standing at this boot, so it must not wait: %q", conf)
	}
	entry, err := os.ReadFile(filepath.Join(mount, "loader", "entries", "liken-A.conf"))
	if err != nil {
		t.Fatal(err)
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
			t.Errorf("the entry is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "initrd=") {
		t.Error("this loader stages the archives itself, so the options must not name them")
	}
}

// A slot that carries an older release names only the archives it
// actually has. systemd-boot refuses an entry whose initrd is missing,
// and an entry that refuses to boot is worse than no fallback: it
// fails after the firmware has already committed to it.
func TestSlotLoaderEntryNamesOnlyTheArchivesTheSlotCarries(t *testing.T) {
	fakeCmdline(t, "console=ttyS0\n")
	mount := slotWithMenu(t)
	if err := os.Remove(filepath.Join(mount, "microcode.cpio")); err != nil {
		t.Fatal(err)
	}

	text := string(slotLoaderEntryText(mount, "B", "node-1"))
	if strings.Contains(text, "microcode.cpio") {
		t.Errorf("the slot has no microcode archive, so the entry must not name one:\n%s", text)
	}
	if !strings.Contains(text, "initrd   /boot.cpio\n") {
		t.Errorf("the archives that are present stay:\n%s", text)
	}
}

func TestWriteSlotLoaderRefusesASlotWithNoMenuProgram(t *testing.T) {
	fakeCmdline(t, "console=ttyS0\n")
	mount := t.TempDir()

	if err := writeSlotLoader(mount, "A", "node-1"); err == nil {
		t.Fatal("a slot with no menu program cannot carry a loader; the caller must hear about it")
	}
	if _, err := os.Stat(filepath.Join(mount, "EFI")); err == nil {
		t.Error("a refusal leaves nothing behind")
	}
}

// The second run must write nothing. A slot is FAT on a real disk, and
// a rewrite on every boot buys nothing.
func TestWriteSlotLoaderWritesNothingTwice(t *testing.T) {
	fakeCmdline(t, "console=ttyS0\n")
	mount := slotWithMenu(t)
	if err := writeSlotLoader(mount, "A", "node-1"); err != nil {
		t.Fatal(err)
	}
	loader := defaultLoaderPath(mount)
	before, err := os.Stat(loader)
	if err != nil {
		t.Fatal(err)
	}

	if err := writeSlotLoader(mount, "A", "node-1"); err != nil {
		t.Fatal(err)
	}

	after, err := os.Stat(loader)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("the loader already agreed, so nothing needed writing")
	}
}

func TestRemoveSlotLoader(t *testing.T) {
	fakeCmdline(t, "console=ttyS0\n")
	mount := slotWithMenu(t)
	if err := writeSlotLoader(mount, "A", "node-1"); err != nil {
		t.Fatal(err)
	}

	removeSlotLoader(mount)

	if _, err := os.Stat(defaultLoaderPath(mount)); !os.IsNotExist(err) {
		t.Error("only the proven slot answers the firmware's default path")
	}
	if _, err := os.Stat(filepath.Join(mount, "loader", "entries", "liken-A.conf")); err != nil {
		t.Error("the entry stays, because the program is what a firmware looks for")
	}
}

func TestOtherSlot(t *testing.T) {
	if otherSlot("A") != "B" || otherSlot("B") != "A" {
		t.Error("the pair has two halves")
	}
}

// A slot whose loader files lost their contents gets them back. This is
// the state a machine reaches when a power cut lands between the write
// and the flush: the entry keeps its name and holds nothing. A firmware
// starts such a loader and then has nothing to boot, so every boot
// compares the contents and not just the names.
func TestWriteSlotLoaderRepairsAnEmptyEntry(t *testing.T) {
	fakeCmdline(t, "console=ttyS0\n")
	mount := slotWithMenu(t)
	if err := writeSlotLoader(mount, "A", "node-1"); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(mount, "loader", "entries", "liken-A.conf")
	if err := os.WriteFile(entry, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeSlotLoader(mount, "A", "node-1"); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "linux    /vmlinuz\n") {
		t.Errorf("the entry is whole again: %q", body)
	}
}
