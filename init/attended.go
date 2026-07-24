package main

// Attended boots: the ones a person is standing at.
//
// liken's boots divide into two kinds, and the difference is not what
// the boot does but who is watching it. A machine booting from its own
// disk at three in the morning has no audience: it must never stop and
// ask a question, and panic=10 with the fall-back slot is its whole
// recovery story. A machine booting from an install stick has an
// audience by construction, because the stick's menu waits forever and
// somebody pressed a key on it.
//
// Every terminal state of an attended boot ends at a held console, so
// the machine's last word stays on screen instead of vanishing behind
// a power-off on a timer. On real hardware a dark screen and a dead
// machine could otherwise mean "done" or "never started".
//
// The two kinds must be told apart by something reliable, and the boot
// words are not it. liken.install, liken.reinstall, and liken.report
// say what a boot does; anything can write them, and the things that
// write them without a person are common (an image build in QEMU, a
// PXE server, a lab Makefile). So the presence of a person is its own
// word on the command line, written by the one thing that can vouch
// for it: the menu.

import (
	"fmt"
	"os"
)

// attendedParam marks a boot that a person started at a keyboard. The
// install stick writes it into every entry of its menu, because picking
// an entry from that menu is the act that proves a person is standing
// at the machine. Nothing else writes it: a command line assembled by a
// script, a Makefile, or a PXE server describes what the boot must do,
// never who is watching it.
const attendedParam = "liken.attended"

// attended reports whether a person is at this machine's console and
// can answer a prompt.
func attended() bool {
	return bootParam(attendedParam)
}

// consoleDevice is the device that /dev/console names: the console the
// kernel opens for init, and the one a person types on. It is a
// variable so tests can point the hold at a file of their own.
var consoleDevice = "/dev/console"

// holdInstallerConsole ends one of the install menu's boots. It reports
// the boot's last word, and, when a person is there to read it, keeps
// that word on screen until they acknowledge it. A finished install and
// a refused one say different things, so the caller supplies the
// message.
//
// The hold happens only on an attended boot. A menu pick is what makes
// a boot attended, and the menu's entries say so with liken.attended.
// An install word alone does not: an image build, a lab guest, and a
// PXE server all write liken.install onto a command line with nobody
// watching, and a machine nobody is watching must never stop at a
// prompt. It would wait for a keypress that never comes, and the caller
// that started it would wait with it, forever. The same reasoning
// covers a headless server, where the console device opens and only the
// read blocks: what tells this code it may wait is the word on the
// command line, not the device answering.
//
// An attended boot's message goes out twice, to two different readers.
// The log copy goes through the kmsg pipeline, which the kernel echoes
// to every console= the command line named, so a person watching any
// one of them reads it in order with the rest of the boot. The direct
// copy goes to the console device, which is the last console= alone
// (the kernel's rule for /dev/console), and that is the device this
// code reads the keypress from. Neither copy reaches both readers, so
// both copies are written.
//
// The direct copy is written immediately. A pause here could only hide
// the race with the still-draining log, and no fixed pause can hide it:
// a serial console at 9600 baud takes seconds to transmit the hardware
// report's proposal. The ordered copy is what puts the message in its
// right place in the boot's story; the direct copy exists to reach the
// screen its reader must answer.
//
// The failure paths also print the disk inventory. The spec's device
// paths resolved against these disks, and an install boot has one more
// contestant in the naming race than the installed machine will: the
// install medium itself. A finished install needs no such evidence, so
// its message stays short.
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
		// With no console to prompt on, there is nobody to wait for,
		// whatever the command line claimed.
		fmt.Fprintf(os.Stderr, "liken: opening the console to wait: %v\n", err)
		return
	}
	defer console.Close()
	fmt.Fprintln(console, message)
	buf := make([]byte, 1)
	_, _ = console.Read(buf)
}
