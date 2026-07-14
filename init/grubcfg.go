package main

// Rendering grub.cfg: the BIOS machine's boot entries.
//
// On a UEFI machine the installer writes two firmware boot entries
// (install.go's writeSlotBootEntry documents the command line flag by
// flag) and the upgrade machinery steers between them with BootNext
// and BootOrder. This file renders the GRUB equivalent: one config on
// the boot home that reads the environment block (grubenv.go) and
// makes the same decisions the firmware would have made.
//
// The config is rendered once, at install time, and is deliberately
// static afterward: everything that changes boot to boot lives in the
// environment block, so steering the machine never means editing this
// file. What is per-machine here is exactly what is per-machine in a
// UEFI entry: the machine's name and its console.
//
// The choreography, mirroring the firmware dialect:
//
//   - try_slot set means the previous boot armed a trial. The config
//     takes it and *consumes it before loading a single kernel byte*
//     (set empty + save_env), exactly as firmware consumes BootNext:
//     any reset after this point — panic, watchdog, power cut — boots
//     default_slot. A crash in the window between save_env and the
//     kernel jump reads as "tried and fell back", wrongly rejecting a
//     release that never ran; armProvingBoot documents why that
//     tradeoff (a fixable false rejection over an unfixable reboot
//     loop) is the right one, and it is the same here.
//
//   - fallback=1 is the BootOrder fall-through. UEFI firmware moves
//     down BootOrder when an entry fails to load; GRUB instead drops
//     to an interactive prompt, which on a headless machine is a
//     hang. With a fallback entry, a chosen slot whose kernel can't
//     be found or loaded falls through to the default slot.
//
//   - The empty-slot default (A) means even a torn or freshly-made
//     environment block boots something: slot A is where the
//     installer put the first release.

import (
	"strings"

	"github.com/liken-sh/liken/machine"
)

func renderGRUBConfig(machineName string, consoles []string) string {
	cfg := "# Rendered by liken at install time; do not edit. Boot-to-boot\n" +
		"# state lives in grubenv, not here (init/grubcfg.go explains).\n"

	// The console: GRUB's own output goes wherever the machine's
	// console is, so boot problems are visible on the same wire the
	// kernel's messages will use. serialConsoleDirectives is empty on
	// a machine with no serial console, and GRUB's default (the VGA
	// text console) stands.
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
	// The two entries differ only in which slot variable they read.
	// Each mirrors the UEFI entry's command line: the kernel and both
	// initrds from the slot found by its label, liken.slot= telling
	// the booted system which half of blue-green it is on, panic=10
	// so a panicking trial resets into the fallback instead of
	// hanging.
	kernelArgs := grubKernelArgs(machineName, consoles)
	cfg += grubMenuEntry("liken (chosen slot)", "$slot", kernelArgs)
	cfg += grubMenuEntry("liken (default slot)", "$default_slot", kernelArgs)
	return cfg
}

func grubMenuEntry(title, slotExpr, kernelArgs string) string {
	return "menuentry '" + title + "' {\n" +
		"    search --no-floppy --label LIKEN-SYS-" + slotExpr + " --set=root\n" +
		"    linux /vmlinuz " + kernelArgs + " liken.slot=" + slotExpr + " panic=10\n" +
		"    initrd /boot.cpio /" + machine.LayerName + "\n" +
		"}\n"
}

// grubKernelArgs assembles the command line both entries share, from
// the same parts writeSlotBootEntry uses: the machine's consoles,
// rdinit, and its name. (The slot and panic arguments are appended
// per entry, since the slot differs.)
func grubKernelArgs(machineName string, consoles []string) string {
	args := ""
	for _, console := range consoles {
		args += console + " "
	}
	return args + "rdinit=/liken liken.machine=" + machineName
}

// serialConsoleDirectives turns console=ttyS<unit>[,<speed>...]
// arguments into GRUB's serial terminal setup, so GRUB's menu and any
// error it prints reach the serial console the machine is actually
// operated from. Other console forms (tty0, hvc0) are left to GRUB's
// default output.
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
			// The kernel's serial options are <speed><parity><bits>;
			// only the leading digits are the speed.
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
