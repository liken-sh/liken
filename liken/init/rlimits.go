package main

// Applying the machine's resource limits.
//
// This is the part of systemd's work that no file can do. A sysctl is
// a file the kernel re-reads, so anything may write one at any time. A
// resource limit is fixed when a process forks, and the only way to
// give k3s a limit is to hold that limit before starting it. So init
// applies these to itself, and every process on the machine inherits
// the result: k3s, containerd, the shims, and the containers below
// them.
//
// The ordering constraint is the whole reason this runs where it does
// in the boot. Init must have read the boot's Machine manifest, so
// that spec.rlimits is known, and it must not yet have started k3s.
// The helpers init ran earlier, mke2fs and modprobe among them, keep
// the kernel's defaults. They are short-lived and open few files, and
// raising a limit for them would mean applying the table before the
// manifest that tunes it.

import (
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/liken-sh/liken/machine"
)

// applyRlimits applies a set of resource limits to init itself. If one
// fails, applyRlimits reports the failure and skips it, rather than
// treating it as fatal. The reasoning matches applySysctls: a limit
// this machine cannot hold should not cost it a boot, and OSRlimits
// ships with the release, so a bad entry would otherwise take a whole
// fleet down at once.
//
// Init calls this twice, once for the limits every liken machine holds
// and once for the Machine spec's own. Printing on both passes shows a
// reader of the boot log what the OS set, and then which of those
// limits this deployment chose to override.
func applyRlimits(rlimits map[string]string) {
	for _, name := range slices.Sorted(maps.Keys(rlimits)) {
		value := rlimits[name]
		if err := machine.ApplyRlimit(name, value); err != nil {
			fmt.Fprintf(os.Stderr, "liken: %v\n", err)
			continue
		}
		fmt.Printf("liken: rlimit %s = %s\n", name, value)
	}
}

// readRlimits reads back every limit that either pass tried to set,
// and reports what the kernel actually holds. Reading back rather than
// echoing what was written is the same discipline status.sysctls
// follows: a limit the kernel clamped or refused shows its real value
// here, so this map is the list of limits that hold, not the list
// somebody asked for.
//
// A name that cannot be read at all is left out, which can only happen
// for a name the spec misspelled. That name already produced an error
// on the console during the pass that tried to apply it.
func readRlimits(sets ...map[string]string) map[string]string {
	names := map[string]bool{}
	for _, set := range sets {
		for name := range set {
			names[name] = true
		}
	}
	if len(names) == 0 {
		return nil
	}
	held := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		value, err := machine.ReadRlimit(name)
		if err != nil {
			continue
		}
		held[name] = value
	}
	if len(held) == 0 {
		return nil
	}
	return held
}
