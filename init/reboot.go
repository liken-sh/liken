package main

// Rebooting on request.
//
// Only PID 1 can shut a machine down correctly. For this reason, the
// operator never reboots the machine itself. Instead, the operator
// writes an intent file (machine/reboot.go describes the channel),
// and init does the rest. Init watches the intent directory with
// inotify: it establishes the watch, scans the directory once, and
// then scans again on every event. The watch-then-scan order closes a
// window. An intent that lands between a scan and the watch would
// otherwise wait for the next event.
//
// An event is only a trigger. The scan decides everything: which
// intent stands, and what it carries. A woken watcher reads the
// directory as it is now, not what an event claimed a moment ago.
// writeAtomic renames a finished file into the directory, so
// IN_MOVED_TO is the event that every intent write produces, and the
// watch asks for it.
//
// The machine guarantees its inotify headroom at boot (osSysctls in
// system.go raises the per-uid watch and instance limits), so a watch
// that fails to start is a real fault, not an expected shortage. Init
// runs the watch as a component of the machine plane, so a failed
// watch surfaces on the console and the plane retries it with backoff.
// There is no polling fallback to hide the fault.
//
// The shutdown sequence runs the dependency stack in reverse order.
// First, it signals every process. (k3s was already stopped
// gracefully, but its containers outlive it, so they get their own
// warning here.) Then it waits a fixed grace period, stops the
// machine plane's own components, detaches the role filesystems,
// syncs, and calls the kernel restart syscall. Under QEMU's
// -no-reboot flag, a restart becomes a clean exit instead of an
// actual reboot. A test with a fixed time limit can use this exit
// as its result.

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/machine"
)

// watchForOperatorIntents watches the operator's channel and delivers
// each intent that lands there. It establishes an inotify watch on the
// directory, runs an initial scan, and then scans again on every wake.
// The watch comes before the first scan, so an intent that lands
// between the scan and the watch cannot slip past unseen.
//
// A watch that cannot start returns the error to the machine plane,
// which reports it and restarts this component. A returned nil tells
// the plane that a reboot intent was delivered and this component's
// work is complete.
func watchForOperatorIntents(ctx context.Context, dir string,
	reboots chan<- machine.RebootIntent, restarts chan<- machine.RestartIntent,
	loads chan<- machine.ModulesIntent) error {
	wake, err := machine.WatchDir(ctx, dir)
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
		case <-wake:
		}
		if scanIntents(dir, reboots, restarts, loads) {
			return nil
		}
	}
}

// scanIntents reads the intent directory once and delivers what it
// finds. It returns true only when it delivered a reboot intent, which
// ends the watch.
//
// The function checks for a reboot intent first. This order is
// deliberate. A reboot re-renders everything that a restart would also
// render. So when both files exist at the same time, the reboot takes
// priority. The restart file is not needed anymore: it disappears with
// the boot's tmpfs, like every reboot intent does.
//
// Delivering a reboot intent ends the watch. A reboot always follows
// it, so there is nothing left to watch for.
//
// A restart intent works differently, because the machine keeps
// running after a restart. The function consumes the restart intent,
// and the watch continues for the rest of the boot. The function
// clears the restart intent file before it delivers the intent to the
// caller. If a crash happens between these two steps, the machine loses
// one restart request. The operator's next pass sends the request
// again. If the function cleared the intent file after delivering it
// instead, a crash between the two steps could cause the machine to
// restart k3s again and again.
//
// The presence of a file is the trigger. The content of a file only
// improves the console message. For this reason, the function honors an
// intent file that it cannot read, instead of ignoring it. (Atomic
// writes make an unreadable file a bug, not a race condition, but a bug
// must not stop the machine from working.)
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
			fmt.Fprintf(os.Stderr, "liken: consuming the restart intent: %v\n", err)
		}
		restarts <- *restart
	}

	// The modules intent behaves like the restart intent: the function
	// consumes it before delivery, and the machine keeps running.
	// Loading modules is the lightest disruption of all: none. The
	// function still honors this intent file even when it cannot read
	// it, because the staged store, not the intent file, holds the
	// truth about what to load. The apply step re-derives everything
	// from the staged store.
	load, err := machine.ReadModulesIntent(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: reading the modules intent: %v\n", err)
		load = &machine.ModulesIntent{Reason: "an unreadable modules intent"}
	}
	if load == nil {
		return false
	}
	if err := machine.ClearModulesIntent(dir); err != nil {
		fmt.Fprintf(os.Stderr, "liken: consuming the modules intent: %v\n", err)
	}
	loads <- *load
	return false
}

// rebootMachine runs init's shutdown sequence: the dependency stack
// in reverse order. The supervisor already stopped k3s gracefully,
// so what remains is its containers, then the machine plane's own
// loops, then the filesystems. Like failBoot, rebootMachine never
// returns, because PID 1 must never exit. If the reboot syscall
// fails, the machine stays in this function until a person
// investigates it.
func rebootMachine(intent machine.RebootIntent) {
	fmt.Printf("liken: rebooting: %s\n", intent.Reason)
	// The firmware actions happen first, while machineState and the
	// boot path's filesystems are still mounted. The function asserts
	// the proven slot before anything else. On a BIOS machine, this
	// assertion also repairs the boot chain on disk. This step must
	// happen on the way down, because a boot path can become damaged
	// while the machine runs. (Cloud hosts rewrite MBRs under running
	// guests.) A damaged boot path would prevent this reboot from
	// coming back up. Arming the proving boot always happens after
	// asserting the proven slot, never before. assertProven clears a
	// stale one-shot flag, and the trial that this reboot might be
	// arming must not appear stale.
	actuator := chooseBootActuator()
	assertProvenSlot(actuator, machine.MachineStateDir)
	// If a release is staged for the other slot, this reboot proves
	// that release. The one-shot trial must be armed before the
	// machine shuts down (see proving.go).
	armProvingBoot(actuator, machine.MachineStateDir, bootParamValue("liken.slot"))
	killEverything()
	// The machine plane stops only after every process ends. The
	// reaper is one of its components, and it must collect exited
	// processes until the very end.
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

// killEverything sends a signal to every process on the machine.
// kill(-1) from PID 1 reaches all processes except init itself.
// SIGTERM comes first, so containers get the same graceful warning
// that k3s got. A fixed grace period is enough. The function does
// not need to track each remaining process, because SIGKILL follows
// and the reaper collects every process either way.
func killEverything() {
	fmt.Println("liken: stopping every remaining process")
	_ = unix.Kill(-1, unix.SIGTERM)
	time.Sleep(5 * time.Second)
	_ = unix.Kill(-1, unix.SIGKILL)
}
