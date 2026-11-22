package main

// The loop runs the sysctl check and the backstop pass on timers.
//
// A pass writes sysctls, and this pod receives no event when another
// process changes one (see machineevents.go). So every sysctlCheckEvery
// the loop reads each parameter that the last pass applied, and runs a
// pass only when one reads differently from the value the pass read
// back after its write. The check compares against the value read back
// and not the value written, because the kernel can return a value in
// another form: a write of 0x10 reads back as 16. When nothing changed,
// the check reads a few files and makes no API request.
//
// The backstop pass finds a bug in this operator: a code path that
// changes state and does not wake the loop. Every event source either
// delivers each change or reports that it lost some. A Kubernetes watch
// resumes from its last version and lists again on a 410. The uevent
// socket returns ENOBUFS and inotify returns IN_Q_OVERFLOW, and the
// readers of both start a pass when that happens. A reader that stops
// ends the process. So a lost change can come only from such a code
// path. After backstopEvery with no other pass, the loop runs a pass,
// and on a correct machine that pass writes nothing. Each write of a
// backstop pass goes to a log line, to the backstop_repairs_total
// metric by step, and to a BackstopRepaired Warning Event on the
// Machine. Fix such a bug by adding the missing wake signal, not by
// shortening backstopEvery.

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/liken/machine"
)

const (
	sysctlCheckEvery = 10 * time.Second
	backstopEvery    = 5 * time.Minute
)

// sysctlCheck holds the value that the last pass read back for each
// parameter it applied, the parameters it could not apply because
// their file did not exist, and the request size of each disk that it
// set (disksettings.go), which also changes with no event.
type sysctlCheck struct {
	applied      map[string]string
	missing      []string
	requestSizes map[string]uint64
}

// drifted reports whether an applied parameter under dir now reads
// differently, or a missing parameter's file now exists. The file of a
// parameter under a network interface appears with the interface, and
// the operator ignores the uevents of a virtual interface (see
// hardware.InventoryEvent), so this check is what finds the new file. A
// parameter that cannot be read counts as drifted, and the pass that
// follows records the failure.
func (c sysctlCheck) drifted(dir string) bool {
	for name, want := range c.applied {
		got, err := machine.ReadSysctl(dir, name)
		if err != nil || !sameSysctlValue(got, want) {
			return true
		}
	}
	for _, name := range c.missing {
		if _, err := machine.ReadSysctl(dir, name); err == nil {
			return true
		}
	}
	return false
}

// backstopDelay returns backstopEvery plus up to a tenth of it at
// random, so that machines that started together do not run their
// backstop passes at the same time. jitter returns a number in [0, 1),
// and nil selects math/rand.
func backstopDelay(jitter func() float64) time.Duration {
	j := rand.Float64
	if jitter != nil {
		j = jitter
	}
	return backstopEvery + time.Duration(j()*float64(backstopEvery)/10)
}

func reportRepairs(writes []string, notes machineEvents, mm *machineMetrics) {
	if len(writes) == 0 {
		return
	}
	steps := slices.Compact(slices.Sorted(slices.Values(writes)))
	fmt.Printf("the backstop pass repaired state that no event reported: %s\n", strings.Join(steps, "; "))
	for _, step := range steps {
		mm.backstopRepaired(step)
	}
	notes.warning(reasonBackstopRepaired, fmt.Sprintf(
		"A backstop pass changed this machine, so a code path in the operator changed state without starting a pass: %s", strings.Join(steps, "; ")))
}
