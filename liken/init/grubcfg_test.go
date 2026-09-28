package main

// Tests for the grub.cfg renderer. The config is generated text, so
// these tests pin the load-bearing lines: the one-shot consumption
// order, the fallback entry, and the command-line match with what
// writeSlotBootEntry gives UEFI machines.

import (
	"strings"
	"testing"
)

func TestGRUBConfigCarriesTheChoreography(t *testing.T) {
	cfg := renderGRUBConfig("node-1", []string{"console=ttyS0"})

	// The one-shot: this test checks that try_slot is consumed
	// (cleared and saved) before any menuentry can run a kernel.
	saveAt := strings.Index(cfg, "save_env try_slot")
	entryAt := strings.Index(cfg, "menuentry")
	if saveAt < 0 || entryAt < 0 || saveAt > entryAt {
		t.Error("try_slot must be consumed before the first menu entry")
	}
	for _, want := range []string{
		"load_env",
		"set slot=$try_slot",
		"set slot=$default_slot",
		"set fallback=1",
		"set timeout=0",
		"search --no-floppy --label LIKEN-SYS-$slot --set=root",
		"search --no-floppy --label LIKEN-SYS-$default_slot --set=root",
		"liken.slot=$slot",
		"liken.slot=$default_slot",
		"initrd /microcode.cpio /boot.cpio /deployment.cpio",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("the config must contain %q", want)
		}
	}
	// A torn or fresh environment block still boots slot A.
	if !strings.Contains(cfg, "set slot=A") {
		t.Error("an empty slot choice must default to A")
	}
}

func TestGRUBConfigKernelLineMirrorsTheUEFIEntry(t *testing.T) {
	cfg := renderGRUBConfig("node-1", []string{"console=ttyS0"})
	want := "linux /vmlinuz console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=$slot panic=10"
	if !strings.Contains(cfg, want) {
		t.Errorf("kernel line should mirror writeSlotBootEntry's arguments:\nwant %q\nin:\n%s", want, cfg)
	}
}

func TestGRUBConfigSerialConsole(t *testing.T) {
	cfg := renderGRUBConfig("node-1", []string{"console=ttyS1,57600n8"})
	if !strings.Contains(cfg, "serial --unit=1 --speed=57600") {
		t.Errorf("a serial console should configure GRUB's serial terminal:\n%s", cfg)
	}
	if !strings.Contains(cfg, "terminal_output serial console") {
		t.Error("GRUB's output should reach both the serial and local consoles")
	}

	// No serial console means no serial directives: GRUB's default
	// output applies.
	plain := renderGRUBConfig("node-1", []string{"console=tty0"})
	if strings.Contains(plain, "serial --unit") {
		t.Error("a machine without a serial console should not configure one")
	}
}
