package main

// A mount's DOME_POLICY follows the observatory's domes: a dome that a
// person deletes unlocks each running mount at once, with no wait for
// the next Configure. A dome declared during a session waits for the
// next one, and its Configure locks the mount again.

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The dome parks while the mount is parked, so the mount's lock
// holds. With the dome deleted, the mount unparks. A dome declared
// again leaves the mount unlocked until the next session locks it.
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
		w.settle()
		if got := w.indi.parked("east-telescope", "Telescope Simulator", "DOME_POLICY"); got != "Ok DOME_IGNORED" {
			t.Errorf("with a dome that waits for the next session, DOME_POLICY is %q, want DOME_IGNORED", got)
		}

		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.gone(observatory.ReservationKind, "east-tonight", 10*time.Minute)
		w.reserve("east-later", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-later", observatory.ReservationReady, 10*time.Minute)
		if got := w.indi.parked("east-telescope", "Telescope Simulator", "DOME_POLICY"); got != "Ok DOME_LOCKS" {
			t.Errorf("in the next session, DOME_POLICY is %q, want DOME_LOCKS", got)
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
