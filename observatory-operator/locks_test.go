package main

// The lock policies of the example's Observatory, which sets both,
// across the observatory's server and the servers of its telescopes.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The relayed reports, as the fake records them.
const (
	domeUnparkedToEast  = "east-telescope Dome Simulator.DOME_PARK PARK=Off UNPARK=On"
	domeParkedToEast    = "east-telescope Dome Simulator.DOME_PARK PARK=On UNPARK=Off"
	mountsUnparkedToLab = "lab-observatory Telescope Simulator.TELESCOPE_PARK PARK=Off UNPARK=On"
	mountsParkedToLab   = "lab-observatory Telescope Simulator.TELESCOPE_PARK PARK=On UNPARK=Off"
)

// bothReady reserves both telescopes and waits until each is Ready.
func bothReady(t *testing.T) *world {
	w := startWorld(t)
	w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
	w.reserve("west-tonight", map[string]any{"telescope": "west", "holder": "desktop"})
	w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
	w.phase("west-tonight", observatory.ReservationReady, 10*time.Minute)
	return w
}

// relayedLast waits until the last report that a server received
// about one property is want.
func (w *world) relayedLast(server, property, want string) {
	w.t.Helper()
	w.until(time.Minute, "the last relay to "+server+" is not "+want, func() bool {
		var last string
		for _, r := range w.indi.relays() {
			if strings.HasPrefix(r, server+" ") && strings.Contains(r, "."+property+" ") {
				last = r
			}
		}
		return last == want
	})
}

func TestTheOperatorWritesBothLockPolicies(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := bothReady(t)
		changes := w.indi.changes()
		for _, want := range []string{
			"lab-observatory Dome Simulator.MOUNT_POLICY MOUNT_IGNORED=Off MOUNT_LOCKS=On",
			"east-telescope Telescope Simulator.DOME_POLICY DOME_IGNORED=Off DOME_LOCKS=On",
			"west-telescope Telescope Simulator.DOME_POLICY DOME_IGNORED=Off DOME_LOCKS=On",
		} {
			if !slices.Contains(changes, want) {
				t.Errorf("no change %q", want)
			}
		}
		w.relayedLast("east-telescope", "DOME_PARK", domeUnparkedToEast)
		w.relayedLast("west-telescope", "DOME_PARK", strings.Replace(domeUnparkedToEast, "east", "west", 1))
		w.relayedLast("lab-observatory", "TELESCOPE_PARK", mountsUnparkedToLab)
	})
}

// The dome snoops one mount, so the operator relays one report for
// both: unparked while either mount is unparked.
func TestTheDomeDoesNotParkWhileAnyMountIsUnparked(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := bothReady(t)
		w.indi.ask("east-telescope", "Telescope Simulator", "TELESCOPE_PARK", "PARK=On", "UNPARK=Off")
		synctest.Wait()
		w.relayedLast("lab-observatory", "TELESCOPE_PARK", mountsUnparkedToLab)
		w.indi.ask("lab-observatory", "Dome Simulator", "DOME_PARK", "PARK=On", "UNPARK=Off")
		if got := w.indi.parked("lab-observatory", "Dome Simulator", "DOME_PARK"); got != "Alert UNPARK" {
			t.Fatalf("DOME_PARK is %q, want Alert with UNPARK On", got)
		}
		w.until(time.Minute, "no Warning about the dome", func() bool {
			return len(eventsAbout(w.api, observatory.DomeKind, "lab", reasonDomeParkRefused)) == 1
		})
		want := "DomeParkRefused: Dome lab refused to park: Mount west is unparked or moving, and Observatory lab sets mountLocksDome, so the dome stays unparked until every mount parks"
		if got := eventsAbout(w.api, observatory.DomeKind, "lab", reasonDomeParkRefused); got[0] != want {
			t.Errorf("event = %q\nwant %q", got[0], want)
		}

		w.indi.ask("west-telescope", "Telescope Simulator", "TELESCOPE_PARK", "PARK=On", "UNPARK=Off")
		w.relayedLast("lab-observatory", "TELESCOPE_PARK", mountsParkedToLab)
		w.indi.ask("lab-observatory", "Dome Simulator", "DOME_PARK", "PARK=On", "UNPARK=Off")
		if got := w.indi.parked("lab-observatory", "Dome Simulator", "DOME_PARK"); got != "Ok PARK" {
			t.Errorf("DOME_PARK is %q, want Ok with PARK On", got)
		}
	})
}

func TestAMountDoesNotUnparkWhileTheDomeIsParked(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := bothReady(t)
		for _, server := range []string{"east-telescope", "west-telescope"} {
			w.indi.ask(server, "Telescope Simulator", "TELESCOPE_PARK", "PARK=On", "UNPARK=Off")
		}
		w.relayedLast("lab-observatory", "TELESCOPE_PARK", mountsParkedToLab)
		w.indi.ask("lab-observatory", "Dome Simulator", "DOME_PARK", "PARK=On", "UNPARK=Off")
		w.relayedLast("east-telescope", "DOME_PARK", domeParkedToEast)

		w.indi.ask("east-telescope", "Telescope Simulator", "TELESCOPE_PARK", "PARK=Off", "UNPARK=On")
		if got := w.indi.parked("east-telescope", "Telescope Simulator", "TELESCOPE_PARK"); got != "Alert PARK" {
			t.Fatalf("TELESCOPE_PARK is %q, want Alert with PARK On", got)
		}
		w.until(time.Minute, "no Warning about the mount", func() bool {
			return len(eventsAbout(w.api, observatory.MountKind, "east", reasonMountUnparkRefused)) == 1
		})
		want := "MountUnparkRefused: Mount east refused to unpark: Dome lab is parked or moving, and Observatory lab sets domeLocksMount, so the mount stays parked until the dome unparks"
		if got := eventsAbout(w.api, observatory.MountKind, "east", reasonMountUnparkRefused); got[0] != want {
			t.Errorf("event = %q\nwant %q", got[0], want)
		}

		w.indi.ask("lab-observatory", "Dome Simulator", "DOME_PARK", "PARK=Off", "UNPARK=On")
		w.relayedLast("east-telescope", "DOME_PARK", domeUnparkedToEast)
		w.indi.ask("east-telescope", "Telescope Simulator", "TELESCOPE_PARK", "PARK=Off", "UNPARK=On")
		if got := w.indi.parked("east-telescope", "Telescope Simulator", "TELESCOPE_PARK"); got != "Ok UNPARK" {
			t.Errorf("TELESCOPE_PARK is %q, want Ok with UNPARK On", got)
		}
	})
}

// A held telescope's mount whose driver stops reporting may be
// unparked, so it keeps the dome from parking.
func TestAMountThatStopsReportingKeepsTheDomeUnparked(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := bothReady(t)
		for _, server := range []string{"east-telescope", "west-telescope"} {
			w.indi.ask(server, "Telescope Simulator", "TELESCOPE_PARK", "PARK=On", "UNPARK=Off")
		}
		w.relayedLast("lab-observatory", "TELESCOPE_PARK", mountsParkedToLab)
		w.api.holdPending("west-mount")
		w.api.deleteNamed(podsCollection, "west-mount")
		w.relayedLast("lab-observatory", "TELESCOPE_PARK", mountsUnparkedToLab)
		w.indi.ask("lab-observatory", "Dome Simulator", "DOME_PARK", "PARK=On", "UNPARK=Off")
		if got := w.indi.parked("lab-observatory", "Dome Simulator", "DOME_PARK"); got != "Alert UNPARK" {
			t.Errorf("DOME_PARK is %q, want Alert with UNPARK On", got)
		}
		w.until(time.Minute, "the condition does not wait for the mount", func() bool {
			c := siteLocks(t, w)
			return c.Reason == reasonLocksWaiting && c.Message == "Waiting for Mount west to report TELESCOPE_PARK"
		})
	})
}

// A driver that restarts forgets what it snooped, and a mount reads a
// report only while it is connected. So the operator relays the dome's
// state again once the new driver connects.
func TestARestartedMountIsRelayedTheDomeAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := bothReady(t)
		for _, server := range []string{"east-telescope", "west-telescope"} {
			w.indi.ask(server, "Telescope Simulator", "TELESCOPE_PARK", "PARK=On", "UNPARK=Off")
		}
		w.relayedLast("lab-observatory", "TELESCOPE_PARK", mountsParkedToLab)
		w.indi.ask("lab-observatory", "Dome Simulator", "DOME_PARK", "PARK=On", "UNPARK=Off")
		w.relayedLast("east-telescope", "DOME_PARK", domeParkedToEast)

		// The new driver does not answer the operator's connect, so the
		// report that the operator relays at once reaches a mount that
		// ignores it. A person then connects the mount.
		w.indi.hold("Telescope Simulator", "CONNECTION")
		connects := w.indi.count("east-telescope", "Telescope Simulator.CONNECTION")
		w.api.deleteNamed(podsCollection, "east-mount")
		w.until(time.Minute, "the operator does not connect the new mount", func() bool {
			return w.indi.count("east-telescope", "Telescope Simulator.CONNECTION") > connects
		})
		w.relayedLast("east-telescope", "DOME_PARK", domeParkedToEast)
		w.indi.release("Telescope Simulator", "CONNECTION")
		w.indi.ask("east-telescope", "Telescope Simulator", "CONNECTION", "CONNECT=On", "DISCONNECT=Off")
		synctest.Wait()
		w.indi.ask("east-telescope", "Telescope Simulator", "TELESCOPE_PARK", "PARK=On", "UNPARK=Off")
		w.indi.ask("east-telescope", "Telescope Simulator", "TELESCOPE_PARK", "PARK=Off", "UNPARK=On")
		if got := w.indi.parked("east-telescope", "Telescope Simulator", "TELESCOPE_PARK"); got != "Alert PARK" {
			t.Errorf("TELESCOPE_PARK is %q, want Alert with PARK On", got)
		}
	})
}

// The Observatory's condition says what the operator relays, and that
// the drivers keep the last relay while the operator is down.
func TestTheObservatoryReportsTheRelay(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.until(time.Minute, "the condition is not Idle", func() bool {
			return siteLocks(t, w).Reason == reasonLocksIdle
		})
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.until(time.Minute, "the condition is not Relayed", func() bool {
			return siteLocks(t, w).Reason == reasonLocksRelayed
		})
		want := "Relayed to Mount east that every dome is unparked: Dome lab; relayed to Dome lab that a mount is unparked or moving: Mount east. " +
			"While observatory-operator is down, each driver keeps the last state that the operator relayed"
		if c := siteLocks(t, w); c.Status != observatory.ConditionTrue || c.Message != want {
			t.Errorf("LocksRelayed = %s %q\nwant True %q", c.Status, c.Message, want)
		}

		site, _ := w.api.object(kindCollection(observatory.ObservatoryKind), "lab")
		delete(site["spec"].(map[string]any), "policies")
		w.api.put(kindCollection(observatory.ObservatoryKind), site)
		w.until(time.Minute, "the condition is still there", func() bool {
			return siteLocks(t, w).Type == ""
		})
	})
}

func siteLocks(t *testing.T, w *world) observatory.Condition {
	site, _ := decode[observatory.Observatory](t, w.api, kindCollection(observatory.ObservatoryKind), "lab")
	return conditionOf(site.Status.Conditions, observatory.ConditionLocksRelayed)
}

// The last release parks the dome after Secure parked both mounts and
// their drivers stopped, so the lock lets the dome park.
func TestTheLastReleaseParksTheDome(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := bothReady(t)
		for _, name := range []string{"east-tonight", "west-tonight"} {
			w.api.deleteNamed(kindCollection(observatory.ReservationKind), name)
		}
		w.until(10*time.Minute, "a reservation is still there", func() bool {
			_, east := w.reservation("east-tonight")
			_, west := w.reservation("west-tonight")
			return !east && !west
		})
		if !slices.Contains(w.indi.changes(), "lab-observatory Dome Simulator.DOME_PARK PARK=On UNPARK=Off") {
			t.Error("the dome was not asked to park")
		}
		if got := eventsAbout(w.api, observatory.DomeKind, "lab", reasonDomeParkRefused); len(got) != 0 {
			t.Errorf("events = %v, want none", got)
		}
	})
}
