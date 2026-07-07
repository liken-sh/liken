package main

// Rebooting on request.
//
// Only PID 1 can take a machine down properly, so the operator never
// reboots anything itself: it writes an intent file (machine/reboot.go
// describes the channel) and init does the rest. The watching is a
// 2-second poll, deliberately chosen over inotify: it's a few lines
// anyone can read, and a 2-second delay is nothing next to the reboot
// it triggers.
//
// The shutdown sequence runs the dependency stack in reverse: signal
// every process (k3s was already stopped gracefully, but its
// containers outlive it and get their own warning here) and wait a
// fixed grace period, stop the machine plane's own components, detach
// the role filesystems, sync, and ask the kernel to restart. Under
// QEMU's -no-reboot flag a restart becomes a clean exit instead,
// which is what a bounded test run wants to observe.

import (
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	"golang.org/x/sys/unix"

	"github.com/chrisguidry/liken/machine"
)

// watchForRebootIntent polls the operator's channel and delivers at
// most one intent: a reboot always follows, so there is nothing left
// to watch for afterward, and returning nil tells the machine plane
// this component's work is complete. The file's presence is the
// trigger; its content only improves the console message, so an
// unreadable intent is honored rather than stranding a requested
// reboot (atomic writes make that case a bug, not a race, but a bug
// must not wedge the machine). The directory and interval are
// parameters so tests can watch a tempdir quickly; the boot passes
// the real channel.
func watchForRebootIntent(ctx context.Context, dir string, interval time.Duration, requests chan<- machine.RebootIntent) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
		intent, err := machine.ReadRebootIntent(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: reading the reboot intent: %v\n", err)
			intent = &machine.RebootIntent{Reason: "an unreadable reboot intent"}
		}
		if intent == nil {
			continue
		}
		requests <- *intent
		return nil
	}
}

// rebootMachine is init's shutdown sequence, the dependency stack in
// reverse: k3s was already stopped gracefully by the supervisor, so
// what remains is its containers, then the machine plane's own loops,
// then the filesystems. Like failBoot, it never returns: PID 1 must
// never exit, so if the reboot syscall itself fails, the machine
// parks here for a person to investigate.
func rebootMachine(intent machine.RebootIntent) {
	fmt.Printf("liken: rebooting: %s\n", intent.Reason)
	// The firmware conversation happens first, while machineState and
	// efivarfs are still mounted: if a release is staged for the other
	// slot, this reboot is its proving boot, and the firmware's
	// BootNext must be armed before the machine goes down
	// (proving.go).
	armProvingBoot(efiVarsDir, machine.MachineStateDir, bootParamValue("liken.slot"))
	killEverything()
	// The machine plane stops only after every process is dead: the
	// reaper is one of its components, and exited processes need
	// collecting right up to the end.
	plane.shutdown(10 * time.Second)
	unmountRoles()
	unix.Sync()
	if err := unix.Reboot(unix.LINUX_REBOOT_CMD_RESTART); err != nil {
		fmt.Fprintf(os.Stderr, "liken: reboot failed: %v\n", err)
	}
	for {
		time.Sleep(time.Hour)
	}
}

// killEverything signals every process on the machine: kill(-1) from
// PID 1 reaches all of them (except init itself). SIGTERM comes
// first, so containers get the same graceful warning k3s got. A
// fixed grace period is enough; there is no need to track every
// straggler, because SIGKILL follows and the reaper collects them
// all either way.
func killEverything() {
	fmt.Println("liken: stopping every remaining process")
	_ = unix.Kill(-1, unix.SIGTERM)
	time.Sleep(5 * time.Second)
	_ = unix.Kill(-1, unix.SIGKILL)
}

// unmountRoles detaches every role filesystem. MNT_DETACH (a lazy
// unmount) rather than a plain one: a just-killed container's mount
// namespace can pin a filesystem for a moment longer, and lazy
// detachment lets the kernel finish the job as those references
// drain, after the sync has already made the data safe.
func unmountRoles() {
	for _, name := range slices.Backward(machine.StorageRoleNames) {
		target := roleMounts[name].path
		if err := unix.Unmount(target, unix.MNT_DETACH); err == nil {
			fmt.Printf("liken: unmounted %s\n", target)
		}
	}
}
