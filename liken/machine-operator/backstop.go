package main

// The two timers of a settled machine: the check of the sysctls, and
// the backstop pass.
//
// The sysctls are the one state a pass writes that sends this pod no
// event when another process changes it (machineevents.go). So every
// sysctlCheckEvery the loop reads each parameter that the last pass
// applied, and runs a pass only when one reads other than the value the
// pass read back after its write. The comparison is against what the
// kernel reported, not against the value the pass wrote, because the
// kernel reports some values in another form: 0x10 reads back as 16. A
// settled machine pays a few small reads, and no pass and no request.
//
// The backstop covers a wake that the operator's own code forgot to
// send. Every event source the operator uses either delivers each
// change or reports that it lost some: a Kubernetes watch resumes from
// its last version and lists again on a 410, the uevent socket reports
// ENOBUFS and inotify IN_Q_OVERFLOW, and both readers wake a pass on
// it, and a reader that stops ends the process. So a lost change comes
// from a path in this program that changes state and sends no wake. A
// pass runs after backstopEvery with no other pass, and on a correct
// machine it writes nothing. A backstop pass that writes anything found
// that bug, and the operator reports each write: a log line, the
// backstop_repairs_total counter by step, and a BackstopRepaired
// Warning Event on the Machine. The fix for a repair is the missing
// wake, not a shorter backstop.

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

// sysctlCheck is what the check of the sysctls reads: what the last
// pass read back for each parameter it applied, and the parameters it
// could not apply because their file did not exist.
type sysctlCheck struct {
	applied map[string]string
	missing []string
}

// drifted answers whether a parameter that the last pass applied now
// reads other than the value the pass read back, or a parameter whose
// file did not exist now has one. A parameter under a network interface
// gets its file when the interface appears, and an interface under
// /devices/virtual sends no uevent this operator hears (hardware's
// InventoryEvent), so the check is what applies it. A parameter that
// does not read at all counts as drifted, and the pass it starts records
// the failure.
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

// backstopDelay answers backstopEvery with up to a tenth more at
// random, so the machines of a fleet that started together do not run
// their backstop passes at the same moments. jitter answers a number in
// [0, 1), and nil means math/rand.
func backstopDelay(jitter func() float64) time.Duration {
	j := rand.Float64
	if jitter != nil {
		j = jitter
	}
	return backstopEvery + time.Duration(j()*float64(backstopEvery)/10)
}

// reportRepairs reports each write of a backstop pass.
func reportRepairs(writes []string, notes machineEvents, mm *machineMetrics) {
	if len(writes) == 0 {
		return
	}
	steps := slices.Compact(slices.Sorted(slices.Values(writes)))
	fmt.Printf("the backstop pass repaired state that no wake reported: %s\n", strings.Join(steps, "; "))
	for _, step := range steps {
		mm.backstopRepaired(step)
	}
	notes.warning(reasonBackstopRepaired, fmt.Sprintf(
		"a pass that no event started changed this machine, so a wake is missing: %s", strings.Join(steps, "; ")))
}
