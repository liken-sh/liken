package main

// An attended boot is one that a person chose from the install
// stick's menu. When an install or a hardware report ends, an attended
// boot waits for a keypress, so its last message stays on screen.
// Without the wait, the machine powers off or reboots at once, and on
// real hardware a dark screen can mean either "done" or "never
// started".
//
// An unattended boot must never wait for input. A machine that boots
// from its own disk has nobody at the console, and when it fails, the
// kernel's panic=10 reboots it into the fallback slot.
//
// The boot parameters liken.install, liken.reinstall, and liken.report
// do not show whether a person is present, because an image build in
// QEMU, a PXE server, or a Makefile can set them. So each entry of the
// install stick's menu also sets liken.attended. A person must pick an
// entry from the menu, so the parameter shows that a person is there.

import (
	"fmt"
	"os"
)

// attendedParam must not be set on a command line that a script, a
// Makefile, or a PXE server writes.
const attendedParam = "liken.attended"

func attended() bool {
	return bootParam(attendedParam)
}

// consoleDevice is the console that the kernel opened for init, which
// is the last console= on the command line. Tests substitute a file.
var consoleDevice = "/dev/console"

// holdInstallerConsole prints the last message of an install or report
// boot and, on an attended boot, waits for a keypress on the console.
// When inventory is true, it first lists the disks, because the device
// paths in the spec were matched against these disks. The list includes
// the install stick, which the installed machine will not have.
//
// Only liken.attended allows the wait. Whether the console opens does
// not show that a person is present: on a headless server the console
// opens, and the read blocks forever.
//
// An attended boot prints the message twice. The copy on stderr goes
// through kmsg, which the kernel writes to every console= device, so
// it appears in order with the rest of the boot log. The copy written
// to consoleDevice reaches the console that the keypress is read from
// at once, because on a slow serial console the log can take seconds
// to catch up, and no fixed pause is long enough.
func holdInstallerConsole(message string, inventory bool) {
	if inventory {
		reportBlockDevices()
	}
	fmt.Fprintln(os.Stderr, message)
	if !attended() {
		return
	}

	console, err := os.OpenFile(consoleDevice, os.O_RDWR, 0)
	if err != nil {
		// There is no console to prompt on, so do not wait.
		fmt.Fprintf(os.Stderr, "liken: opening the console to wait for a keypress, so continuing without the wait: %v\n", err)
		return
	}
	defer console.Close()
	fmt.Fprintln(console, message)
	buf := make([]byte, 1)
	_, _ = console.Read(buf)
}
