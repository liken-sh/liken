package main

// Only PID 1 can shut a machine down correctly, so the machine
// operator never reboots the machine itself. It writes an intent file
// (machine/reboot.go defines them) and init acts on it.
//
// Init opens an inotify watch on the intent directory first, then
// scans the directory once, then scans again on every event. Because
// the watch opens before the first scan, an intent written during the
// scan still produces an event. A scan reads the directory as it is
// and ignores what the event reported. The operator writes each
// intent with writeAtomic, which renames a finished file into the
// directory, so every write produces IN_MOVED_TO.
//
// The machine raises the per-user inotify limits at boot (osSysctls in
// system.go), so a watch that fails to start is a fault. The machine
// plane reports it on the console and retries with backoff. There is
// no polling fallback, because it would hide the fault.

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// watchIntentDir is a variable so a test can substitute a watch that
// stops.
var watchIntentDir = machine.WatchDir

// watchForOperatorIntents sends each intent that the operator writes
// to dir on the channel for its kind. It returns an error when the
// watch cannot start or stops, so the machine plane reports it and
// starts the watch again. It returns nil when ctx ends or after it
// sends a reboot intent, which tells the plane not to restart it.
func watchForOperatorIntents(ctx context.Context, dir string,
	reboots chan<- machine.RebootIntent, restarts chan<- machine.RestartIntent,
	loads chan<- machine.ModulesIntent) error {
	wake, err := watchIntentDir(ctx, dir)
	if err != nil {
		return fmt.Errorf("watching %s: %w", dir, err)
	}
	if scanIntents(dir, reboots, restarts, loads) {
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-wake:
			if !ok {
				return fmt.Errorf("the inotify watch on %s ended", dir)
			}
		}
		if scanIntents(dir, reboots, restarts, loads) {
			return nil
		}
	}
}

// scanIntents reads the intent directory once and sends each intent it
// finds. It returns true only after it sends a reboot intent.
//
// When both a reboot and a restart intent exist, it sends only the
// reboot, because a reboot applies every change that a k3s restart
// would. The restart file is on tmpfs, so the reboot deletes it.
//
// The machine keeps running after a restart or a modules intent, so
// the scan deletes each of those files before it sends the intent.
// When init crashes between the two steps, the request is lost, and
// the operator writes it again on its next pass. Sending first would
// restart k3s again after each crash, because the file would remain.
//
// The existence of an intent file is the request, and its content
// only supplies the reason that the console shows. A file that cannot
// be read is sent with a placeholder reason, so a corrupt file cannot
// stop a reboot.
func scanIntents(dir string, reboots chan<- machine.RebootIntent,
	restarts chan<- machine.RestartIntent, loads chan<- machine.ModulesIntent) bool {
	intent, err := machine.ReadRebootIntent(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: reading the reboot intent: %v\n", err)
		intent = &machine.RebootIntent{Reason: "an unreadable reboot intent"}
	}
	if intent != nil {
		reboots <- *intent
		return true
	}

	restart, err := machine.ReadRestartIntent(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: reading the restart intent: %v\n", err)
		restart = &machine.RestartIntent{Reason: "an unreadable restart intent"}
	}
	if restart != nil {
		if err := machine.ClearRestartIntent(dir); err != nil {
			fmt.Fprintf(os.Stderr, "liken: deleting the restart intent file: %v\n", err)
		}
		restarts <- *restart
	}

	// An unreadable modules intent is still sent, because the step that
	// loads the modules reads the list from the staged manifest, not
	// from the intent.
	load, err := machine.ReadModulesIntent(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: reading the modules intent: %v\n", err)
		load = &machine.ModulesIntent{Reason: "an unreadable modules intent"}
	}
	if load == nil {
		return false
	}
	if err := machine.ClearModulesIntent(dir); err != nil {
		fmt.Fprintf(os.Stderr, "liken: deleting the modules intent file: %v\n", err)
	}
	loads <- *load
	return false
}

// rebootMachine stops the machine in the reverse of the order it
// started and restarts it. It never returns, because PID 1 must not
// exit. When the reboot syscall fails, the machine stays up for a
// person to investigate. Under QEMU's -no-reboot flag the restart
// makes QEMU exit, so a test with a time limit can wait for that.
func rebootMachine(intent machine.RebootIntent) {
	fmt.Printf("liken: rebooting: %s\n", intent.Reason)
	// The flag is never cleared. It stops the proving watch, which can
	// still run, from undoing the trial boot that is armed below.
	shuttingDown.Store(true)
	// Each supplicant runs under a loop that restarts it, so the
	// SIGTERM in killEverything would only make the loop start it again.
	// Stopping it through its loop lets it deauthenticate once.
	stopSupplicants()
	killEverything()
	// The boot slot and the firmware's boot entries are checked and
	// corrected here, because something can damage them while the
	// machine runs (some cloud hosts rewrite the MBR of a running
	// guest), and then the machine would not boot again. This runs
	// after killEverything, so no process, such as the machine operator
	// downloading a release, can write a slot after the check. See
	// assertAndArmForReboot in proving.go.
	assertAndArmForReboot(chooseBootActuator(), machine.MachineStateDir,
		bootParamValue("liken.slot"), intent.KeepBootNext)
	// The machine plane runs the reaper, so it stops only after
	// killEverything has ended every other process.
	plane.shutdown(10 * time.Second)
	// Remounting read-only records a clean shutdown on filesystems that
	// are still busy at unmount. quiesce.go explains.
	quiesceDisks()
	unmountRoleMounts(unix.MNT_DETACH, false)
	syncLogs()
	unix.Sync()
	if err := unix.Reboot(unix.LINUX_REBOOT_CMD_RESTART); err != nil {
		fmt.Fprintf(os.Stderr, "liken: the reboot system call returned an error, so the machine stays up: %v\n", err)
	}
	for {
		time.Sleep(time.Hour)
	}
}

// killEverything sends SIGTERM to every process except init, waits 5
// seconds, then sends SIGKILL. The containers keep running after k3s
// stops, and the SIGTERM lets them stop cleanly.
// The wait is fixed because init does not track which processes are
// left.
func killEverything() {
	fmt.Println("liken: stopping every remaining process")
	_ = unix.Kill(-1, unix.SIGTERM)
	time.Sleep(5 * time.Second)
	_ = unix.Kill(-1, unix.SIGKILL)
}
