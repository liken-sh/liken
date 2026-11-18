package main

// A child process copies its resource limits from its parent at
// fork, so init sets the limits on itself before it starts k3s, and
// k3s, containerd, the shims, and the containers inherit them. Init
// must first read the Machine manifest, which has spec.rlimits, so
// the programs that init runs before that, such as mke2fs and
// modprobe, run with the kernel's defaults.

import (
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/liken-sh/liken/liken/machine"
)

// applyRlimits prints and skips a limit that fails, as applySysctls
// does, because a bad entry in machine.OSRlimits would otherwise stop
// the boot of every machine on that release. Init calls it first with
// machine.OSRlimits and then with spec.rlimits, so the boot log shows
// which defaults the Machine overrides.
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

// readRlimits returns the current value of each limit named in any
// of the sets. It reads each limit back, so a value that the kernel
// clamped or refused shows as it is. It leaves out a name that is not
// a resource, which applyRlimits already reported.
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
	limits := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		value, err := machine.ReadRlimit(name)
		if err != nil {
			continue
		}
		limits[name] = value
	}
	if len(limits) == 0 {
		return nil
	}
	return limits
}
