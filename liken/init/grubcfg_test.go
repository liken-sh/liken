package main

// The tests check the lines of the generated config that decide how
// the machine boots: when try_slot is cleared, the fallback entry, and
// a kernel command line that matches writeSlotBootEntry's.

import (
	"strings"
	"testing"
)

func TestGRUBConfigSelectsTheSlotAndHasAFallbackEntry(t *testing.T) {
	cfg := renderGRUBConfig("node-1", []string{"console=ttyS0"})

	// try_slot must be cleared and saved before any entry loads a
	// kernel.
	saveAt := strings.Index(cfg, "save_env try_slot")
	entryAt := strings.Index(cfg, "menuentry")
	if saveAt < 0 || entryAt < 0 || saveAt > entryAt {
		t.Errorf("grub.cfg saves try_slot at %d and has its first menuentry at %d, want try_slot cleared before the first menu entry", saveAt, entryAt)
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
			t.Errorf("grub.cfg does not contain %q, want it:\n%s", want, cfg)
		}
	}
	// An empty slot variable boots slot A.
	if !strings.Contains(cfg, "set slot=A") {
		t.Errorf("grub.cfg has no \"set slot=A\", want slot A when the slot variable is empty:\n%s", cfg)
	}
}

func TestGRUBConfigKernelLineMatchesTheUEFIEntry(t *testing.T) {
	cfg := renderGRUBConfig("node-1", []string{"console=ttyS0"})
	want := "linux /vmlinuz console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=$slot panic=10"
	if !strings.Contains(cfg, want) {
		t.Errorf("grub.cfg has no kernel line with the arguments of writeSlotBootEntry, want %q:\n%s", want, cfg)
	}
}

func TestGRUBConfigSerialConsole(t *testing.T) {
	cfg := renderGRUBConfig("node-1", []string{"console=ttyS1,57600n8"})
	if !strings.Contains(cfg, "serial --unit=1 --speed=57600") {
		t.Errorf("grub.cfg has no \"serial --unit=1 --speed=57600\", want GRUB's serial terminal set up for the serial console:\n%s", cfg)
	}
	if !strings.Contains(cfg, "terminal_output serial console") {
		t.Errorf("grub.cfg has no \"terminal_output serial console\", want GRUB's output on both the serial and the local console:\n%s", cfg)
	}

	// Without a serial console, GRUB keeps its default output.
	plain := renderGRUBConfig("node-1", []string{"console=tty0"})
	if strings.Contains(plain, "serial --unit") {
		t.Errorf("grub.cfg has \"serial --unit\", want no serial terminal on a machine with no serial console:\n%s", plain)
	}
}
