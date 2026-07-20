package main

// Rebooting on request.
//
// Only PID 1 can shut a machine down correctly. For this reason, the
// operator never reboots the machine itself. Instead, the operator
// writes an intent file (machine/reboot.go describes the channel),
// and init does the rest. Init checks for this file with a 2-second
// poll. This method is deliberate: it uses a few lines that anyone
// can read, instead of inotify. A 2-second delay is small compared
// to the reboot that follows it.
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

// watchForOperatorIntents polls the operator's channel for two kinds
// of disruption: a reboot and a restart.
//
// The function checks for a reboot intent first on every tick. This
// order is deliberate. A reboot re-renders everything that a restart
// would also render. So when both files exist at the same time, the
// reboot takes priority. The restart file is not needed anymore: it
// disappears with the boot's tmpfs, like every reboot intent does.
//
// Delivering a reboot intent ends the watch. A reboot always follows
// it, so there is nothing left to watch for. When the function
// returns nil, it tells the machine plane that this component's work
// is complete.
//
// A restart intent works differently, because the machine keeps
// running after a restart. The function consumes the restart intent
// and keeps watching for the rest of the boot. The function clears
// the restart intent file before it delivers the intent to the
// caller. If a crash happens between these two steps, the machine
// loses one restart request. The operator's next pass sends the
// request again. If the function cleared the intent file after
// delivering it instead, a crash between the two steps could cause
// the machine to restart k3s again and again.
//
// The presence of a file is the trigger. The content of a file only
// improves the console message. For this reason, the function honors
// an intent file that it cannot read, instead of ignoring it. (Atomic
// writes make an unreadable file a bug, not a race condition, but a
// bug must not stop the machine from working.) The directory and the
// poll interval are parameters, so tests can watch a temporary
// directory with fast polls. The boot passes the real channel.
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

		// The modules intent behaves like the restart intent: the
		// function consumes it before delivery, and the machine keeps
		// running. Loading modules is the lightest disruption of all:
		// none. The function still honors this intent file even when it
		// cannot read it, because the staged store, not the intent
		// file, holds the truth about what to load. The apply step
		// re-derives everything from the staged store.
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
