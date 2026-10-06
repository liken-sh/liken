package main

// A mount's DOME_POLICY follows the observatory's domes at once: a
// dome that goes away unlocks each running mount, and a dome that
// comes locks them again, with no wait for the next Configure.

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The dome parks while the mount is parked, so the mount's lock
// holds. With the dome deleted, the mount unparks, and with a dome
// declared again, the mount is locked again and keeps the lock in its
// saved configuration.
func TestAMountsDomePolicyFollowsTheDomes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.ask("east-telescope", "Telescope Simulator", "TELESCOPE_PARK", "PARK=On", "UNPARK=Off")
		w.relayedLast("lab-observatory", "TELESCOPE_PARK", mountsParkedToLab)
		w.indi.ask("lab-observatory", "Dome Simulator", "DOME_PARK", "PARK=On", "UNPARK=Off")
		w.relayedLast("east-telescope", "DOME_PARK", domeParkedToEast)
		dome, _ := w.api.object(kindCollection(observatory.DomeKind), "lab")

		w.api.deleteNamed(kindCollection(observatory.DomeKind), "lab")
		w.until(time.Minute, "the mount is still locked", func() bool {
			return w.indi.parked("east-telescope", "Telescope Simulator", "DOME_POLICY") == "Ok DOME_IGNORED"
		})
		w.indi.ask("east-telescope", "Telescope Simulator", "TELESCOPE_PARK", "PARK=Off", "UNPARK=On")
		if got := w.indi.parked("east-telescope", "Telescope Simulator", "TELESCOPE_PARK"); got != "Ok UNPARK" {
			t.Errorf("with no dome, TELESCOPE_PARK is %q, want Ok with UNPARK On", got)
		}

		delete(dome["metadata"].(map[string]any), "resourceVersion")
		w.api.put(kindCollection(observatory.DomeKind), dome)
		w.until(time.Minute, "the mount is not locked again", func() bool {
			return w.indi.parked("east-telescope", "Telescope Simulator", "DOME_POLICY") == "Ok DOME_LOCKS"
		})
		w.indi.look("east-telescope")
		if got := w.indi.parked("east-telescope", "Telescope Simulator", "DOME_POLICY"); got != "Ok DOME_LOCKS" {
			t.Errorf("after a client looks, DOME_POLICY is %q, want the saved DOME_LOCKS", got)
		}
	})
}

// Activation writes a mount's DOME_POLICY once, in Configure, and saves
// the driver's configuration once for it. A driver rewrites its
// configuration file on each save.
func TestActivationWritesAMountsDomePolicyOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		writes, saves := 0, 0
		for _, c := range w.indi.changes() {
			switch {
			case strings.HasPrefix(c, "east-telescope Telescope Simulator.DOME_POLICY "):
				writes++
			case strings.HasPrefix(c, "east-telescope Telescope Simulator.CONFIG_PROCESS "):
				saves++
			}
		}
		if writes != 1 || saves != 1 {
			t.Errorf("DOME_POLICY written %d times and the configuration saved %d times, want once each", writes, saves)
		}
	})
}
