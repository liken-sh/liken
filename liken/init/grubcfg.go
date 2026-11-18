package main

// This file renders grub.cfg, which holds the boot entries of a BIOS
// machine.
//
// A UEFI machine gets two firmware boot entries from the installer
// (writeSlotBootEntry in bootentries.go), and upgrades choose between
// them with BootNext and BootOrder. A BIOS machine has no such
// variables, so one grub.cfg on the boot home makes the same choices:
// it reads the environment block (grubenv.go) and picks a slot from it.
//
// The installer writes the config once and nothing edits it later. The
// choice of slot for the next boot is in the environment block. The
// only values specific to a machine are its name and its consoles, as
// in a UEFI entry.
//
//   - When try_slot is set, the previous boot armed a trial of a
//     release. The config clears try_slot and runs save_env before it
//     loads the kernel, the way firmware clears BootNext before it
//     boots the entry. So any reset after that point, such as a panic,
//     a watchdog reset, or a power cut, boots default_slot. A crash
//     after save_env and before the kernel starts counts as a failed
//     trial, and rejects a release that never ran. armProvingBoot
//     explains why that is better than a reboot loop that nobody can
//     clear.
//
//   - fallback=1 replaces the fall-through of BootOrder. When an entry
//     fails to load, UEFI firmware tries the next one, but GRUB stops
//     at an interactive prompt, which hangs a machine with no
//     keyboard. With fallback=1, when the chosen slot's kernel is
//     missing or fails to load, GRUB boots the default slot's entry.
//
//   - An empty slot variable defaults to slot A, where the installer
//     put the first release, so a damaged or new environment block
//     still boots.

import (
	"strings"

	"github.com/liken-sh/liken/liken/machine"
)

// renderGRUBConfig returns the grub.cfg text for a machine.
func renderGRUBConfig(machineName string, consoles []string) string {
	cfg := "# liken writes this file at install time. Do not edit it. The state\n" +
		"# that changes from boot to boot is in grubenv (init/grubcfg.go).\n"

	cfg += serialConsoleDirectives(consoles)

	cfg += `
load_env

if [ -n "$try_slot" ]; then
    set slot=$try_slot
    set try_slot=
    save_env try_slot
else
    set slot=$default_slot
fi
if [ -z "$slot" ]; then
    set slot=A
fi

set default=0
set timeout=0
set fallback=1

`
	// The entries differ only in the slot variable they read. Each one
	// boots from the filesystem labelled LIKEN-SYS-<slot>, with the same
	// command line as a UEFI entry. liken.slot tells the booted system
	// which slot it runs from. panic=10 reboots a panicked kernel after
	// 10 seconds instead of hanging, and because try_slot is already
	// clear, that reboot boots default_slot.
	kernelArgs := grubKernelArgs(machineName, consoles)
	cfg += grubMenuEntry("liken (chosen slot)", "$slot", kernelArgs)
	cfg += grubMenuEntry("liken (default slot)", "$default_slot", kernelArgs)
	return cfg
}

// grubMenuEntry returns one slot's menu entry. GRUB's initrd directive
// concatenates the files in the order given. The microcode cpio comes
// first, as in the initrd= arguments of a UEFI entry, because the
// kernel looks for microcode only at the start of its initrd.
func grubMenuEntry(title, slotExpr, kernelArgs string) string {
	return "menuentry '" + title + "' {\n" +
		"    search --no-floppy --label LIKEN-SYS-" + slotExpr + " --set=root\n" +
		"    linux /vmlinuz " + kernelArgs + " liken.slot=" + slotExpr + " panic=10\n" +
		"    initrd /microcode.cpio /boot.cpio /" + machine.LayerName + "\n" +
		"}\n"
}

// grubKernelArgs returns the kernel arguments that both entries share.
// They match the arguments that writeSlotBootEntry writes for a UEFI
// entry, except liken.slot and panic=10, which grubMenuEntry appends.
func grubKernelArgs(machineName string, consoles []string) string {
	args := ""
	for _, console := range consoles {
		args += console + " "
	}
	return args + "rdinit=/liken liken.machine=" + machineName
}

// serialConsoleDirectives sends GRUB's menu and errors to the first
// console=ttyS<unit>[,<options>] argument, the serial console that the
// kernel also writes to, as well as to GRUB's default console. It
// returns "" when there is no serial console, and GRUB then uses only
// its default console.
func serialConsoleDirectives(consoles []string) string {
	for _, console := range consoles {
		rest, ok := strings.CutPrefix(console, "console=ttyS")
		if !ok {
			continue
		}
		unit, options, _ := strings.Cut(rest, ",")
		if unit == "" {
			continue
		}
		speed := "115200"
		if options != "" {
			// The kernel's options are <speed><parity><bits>, for example
			// 57600n8. The speed is the leading digits.
			digits := options
			for i, r := range options {
				if r < '0' || r > '9' {
					digits = options[:i]
					break
				}
			}
			if digits != "" {
				speed = digits
			}
		}
		return "serial --unit=" + unit + " --speed=" + speed + "\n" +
			"terminal_output serial console\n" +
			"terminal_input serial console\n"
	}
	return ""
}
