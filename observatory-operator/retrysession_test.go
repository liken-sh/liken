package main

// A retry is a person saying that they fixed the spec that failed a
// step, so it records the session again from the specs as they are.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// failThenFix gives a device a driver that no image of the indi build
// holds, so east-tonight fails, then names the device's own driver
// again and asks for a retry.
func failThenFix(w *world, kind observatory.Kind, name, driver string) {
	w.t.Helper()
	w.amend(kind, name, func(spec map[string]any) { spec["driver"] = map[string]any{"name": "indi_fishcamp_ccd"} })
	w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
	w.phase("east-tonight", observatory.ReservationFailed, 15*time.Minute)
	w.amend(kind, name, func(spec map[string]any) { spec["driver"] = map[string]any{"name": driver} })
	w.retry("east-tonight")
}

// The session starts the focuser with the driver that its spec names
// after the retry.
func TestARetryStartsTheSpecThatAPersonFixed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		failThenFix(w, observatory.FocuserKind, "east", "indi_simulator_focus")
		r := w.phase("east-tonight", observatory.ReservationReady, 15*time.Minute)

		if got := r.Status.Session.Devices["Focuser/east"].Driver.Name; got != "indi_simulator_focus" {
			t.Errorf("the record's focuser driver = %q, want indi_simulator_focus", got)
		}
	})
}

// The dome fails east-tonight's StartSite, and the retry records the
// fixed dome. A reservation of west that starts later records the set
// that east-tonight recorded, so it records the fixed dome too.
func TestARetryOfStartSiteRecordsTheFixedSiteForLaterReservations(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		failThenFix(w, observatory.DomeKind, "lab", "indi_simulator_dome")
		w.phase("east-tonight", observatory.ReservationReady, 15*time.Minute)
		w.reserve("west-tonight", map[string]any{"telescope": "west", "holder": "desktop"})
		w.phase("west-tonight", observatory.ReservationReady, 15*time.Minute)

		for _, name := range []string{"east-tonight", "west-tonight"} {
			r, _ := w.reservation(name)
			if got := r.Status.Session.Site["Dome/lab"].Driver.Name; got != "indi_simulator_dome" {
				t.Errorf("the dome driver that %s records = %q, want indi_simulator_dome", name, got)
			}
		}
	})
}

// west-tonight starts while east-tonight's StartSite has failed, so it
// records the dome that failed and fails too. The retry of east-tonight
// records the fixed dome and leaves out the set that west-tonight
// copied, and then the retry of west-tonight records the fixed dome.
func TestARetryOfStartSiteLeavesOutTheSetThatAnotherFailedReservationCopied(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.amend(observatory.DomeKind, "lab", func(spec map[string]any) { spec["driver"] = map[string]any{"name": "indi_fishcamp_ccd"} })
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationFailed, 15*time.Minute)
		w.reserve("west-tonight", map[string]any{"telescope": "west", "holder": "desktop"})
		w.phase("west-tonight", observatory.ReservationFailed, 15*time.Minute)

		w.amend(observatory.DomeKind, "lab", func(spec map[string]any) { spec["driver"] = map[string]any{"name": "indi_simulator_dome"} })
		w.retry("east-tonight")
		w.phase("east-tonight", observatory.ReservationReady, 15*time.Minute)
		w.retry("west-tonight")
		w.phase("west-tonight", observatory.ReservationReady, 15*time.Minute)

		for _, name := range []string{"east-tonight", "west-tonight"} {
			r, _ := w.reservation(name)
			if got := r.Status.Session.Site["Dome/lab"].Driver.Name; got != "indi_simulator_dome" {
				t.Errorf("the dome driver that %s records = %q, want indi_simulator_dome", name, got)
			}
		}
	})
}
