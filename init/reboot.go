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
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/machine"
)

// watchForOperatorIntents polls the operator's channel for both
// disruption kinds. The reboot intent is checked first on every
// tick, deliberately: a reboot re-renders everything a restart
// would, so when both stand, the heavier one wins and the restart
// file vanishes with the boot's tmpfs like every reboot intent
// does. Delivering a reboot ends the watch: a reboot always
// follows, so there is nothing left to watch for, and returning nil
// tells the machine plane this component's work is complete. A
// restart is different. The machine lives on, so the intent is
// consumed and the watch continues for the boot's whole life. The
// clear happens *before* delivery: a crash between the two loses
// one restart, which the operator's next pass re-requests, where
// the reverse order would bounce k3s forever.
//
// The files' presence is the trigger; their content only improves
// the console message, so an unreadable intent of either kind is
// honored rather than stranded (atomic writes make that case a bug,
// not a race, but a bug must not wedge the machine). The directory
// and interval are parameters so tests can watch a tempdir quickly;
// the boot passes the real channel.
func watchForOperatorIntents(ctx context.Context, dir string, interval time.Duration,
	reboots chan<- machine.RebootIntent, restarts chan<- machine.RestartIntent,
	loads chan<- machine.ModulesIntent) error {
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
		if intent != nil {
			reboots <- *intent
			return nil
		}

		restart, err := machine.ReadRestartIntent(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: reading the restart intent: %v\n", err)
			restart = &machine.RestartIntent{Reason: "an unreadable restart intent"}
		}
		if restart != nil {
			if err := machine.ClearRestartIntent(dir); err != nil {
				fmt.Fprintf(os.Stderr, "liken: consuming the restart intent: %v\n", err)
			}
			restarts <- *restart
		}

		// The modules intent lives and dies like the restart intent
		// (consumed before delivery, machine lives on), and takes the
		// lightest disruption of all: none. An unreadable one is still
		// honored, because the staged store — not the intent — is the
		// truth about what to load, and the apply re-derives
		// everything from it.
		load, err := machine.ReadModulesIntent(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: reading the modules intent: %v\n", err)
			load = &machine.ModulesIntent{Reason: "an unreadable modules intent"}
		}
		if load == nil {
			continue
		}
		if err := machine.ClearModulesIntent(dir); err != nil {
			fmt.Fprintf(os.Stderr, "liken: consuming the modules intent: %v\n", err)
		}
		loads <- *load
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
	// the boot path's filesystems are still mounted. The proven slot
	// is asserted before anything else — on a BIOS machine that
	// assertion also heals the boot chain on disk, and it must happen
	// on the way down because a boot path damaged while the machine
	// ran (cloud hosts rewrite MBRs under running guests) would turn
	// this reboot into the one that never comes back. Arming comes
	// after asserting, never before: assertProven clears a stale
	// one-shot, and the trial this reboot may be arming must not read
	// as stale.
	actuator := chooseBootActuator()
	assertProvenSlot(actuator, machine.MachineStateDir)
	// If a release is staged for the other slot, this reboot is its
	// proving boot, and the one-shot trial must be armed before the
	// machine goes down (proving.go).
	armProvingBoot(actuator, machine.MachineStateDir, bootParamValue("liken.slot"))
	killEverything()
	// The machine plane stops only after every process is dead: the
	// reaper is one of its components, and exited processes need
	// collecting right up to the end.
	plane.shutdown(10 * time.Second)
	unmountRoleMounts(unix.MNT_DETACH, false)
	syncLogs()
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
