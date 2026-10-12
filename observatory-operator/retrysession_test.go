package main

// A retry is a person saying that they fixed the spec that failed a
// step, so it records the session again from the specs as they are.

import (
	"fmt"
	"maps"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
	"github.com/liken-sh/liken/observatory-operator/phd2/phd2test"
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

// failGuiderThenRetry fails east-tonight at StartGuider, after its
// Activation step opened the dust cap, applies change, lets PHD2
// connect, and asks for a retry.
func failGuiderThenRetry(w *world, change func(w *world)) {
	w.t.Helper()
	w.guiders.prepare = func(s *phd2test.Server) { s.Refuse = "equipment failed to connect: camera" }
	w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
	w.phase("east-tonight", observatory.ReservationFailed, 15*time.Minute)
	change(w)
	w.guiders.mu.Lock()
	w.guiders.prepare = nil
	for _, g := range w.guiders.running {
		g.phd2.Set(func(s *phd2test.Server) { s.Refuse = "" })
	}
	w.guiders.mu.Unlock()
	w.retry("east-tonight")
}

// A retry that records a spec which moves the open dust cap off the
// telescope leaves the session: the cap closes through its own
// deactivation, its pod goes, and its Event names the retry.
func TestARetryThatMovesADeviceOutOfTheSessionReleasesIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		failGuiderThenRetry(w, func(w *world) { w.moveDevice(observatory.DustCapKind, "east", "", "") })
		w.phase("east-tonight", observatory.ReservationReady, 15*time.Minute)
		w.settle()

		if !slices.Contains(w.indi.changes(), parkCap) {
			t.Errorf("changes = %q, want %q", w.indi.changes(), parkCap)
		}
		if slices.Contains(w.api.names(podsCollection), "east-dustcap") {
			t.Errorf("pods = %v, want no east-dustcap", w.api.names(podsCollection))
		}
		left := "Normal PodDeleted: Deleted pod east-dustcap, because the retry of Reservation east-tonight recorded a spec that takes the DustCap out of its session."
		if got := typedEvents(w.api, observatory.DustCapKind, "east"); !slices.Contains(got, left) {
			t.Errorf("dust cap Events = %q, want %q", got, left)
		}
	})
}

// output answers the change that switches an output of the east
// telescope's Switch on or off.
func output(n int, on bool) string {
	if on {
		return fmt.Sprintf("east-telescope Simulator IO.DIGITAL_OUTPUT_%d OFF=Off ON=On", n)
	}
	return fmt.Sprintf("east-telescope Simulator IO.DIGITAL_OUTPUT_%d OFF=On ON=Off", n)
}

// The main camera fails StartDevices after PowerOn switched on its
// output 1, and a person fixes its driver and moves it to output 4. The
// retry runs PowerOn again, which switches output 4 on, and switches
// off output 1, which no other device draws from. The release switches
// off output 4.
func TestARetryThatChangesADevicesPowerRunsPowerOnAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.amend(observatory.CameraKind, "east-main", func(spec map[string]any) { spec["driver"] = map[string]any{"name": "indi_fishcamp_ccd"} })
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationFailed, 15*time.Minute)
		w.amend(observatory.CameraKind, "east-main", func(spec map[string]any) {
			spec["driver"] = map[string]any{"name": "indi_simulator_ccd"}
			spec["power"] = map[string]any{"switch": "east", "output": 4}
		})
		failed := len(w.indi.changes())
		w.retry("east-tonight")
		w.phase("east-tonight", observatory.ReservationReady, 15*time.Minute)
		retried := slices.Clone(w.indi.changes()[failed:])
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(10*time.Minute, "the Reservation is still there", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
		released := w.indi.changes()[failed+len(retried):]

		for _, want := range []string{output(1, false), output(4, true)} {
			if !slices.Contains(retried, want) {
				t.Errorf("the retry's changes = %q, want %q", retried, want)
			}
		}
		if !slices.Contains(released, output(4, false)) {
			t.Errorf("the release's changes = %q, want %q", released, output(4, false))
		}
	})
}

// west-tonight fails StartSite after it recorded the weather station
// and started its pod on the observatory's server. A person then moves
// the station to the east telescope. east-tonight records the station
// on the observatory's server too, fails, and retries. The station
// stays on the observatory's server, which west-tonight's session
// started it on, so no record names it on the telescope's.
func TestARetryLeavesOutADeviceThatAFailedSiteStarted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.indi.refuse("Weather Simulator", "CONNECTION")
		w.reserve("west-tonight", map[string]any{"telescope": "west", "holder": "desktop"})
		w.phase("west-tonight", observatory.ReservationFailed, 15*time.Minute)
		w.indi.accept("Weather Simulator", "CONNECTION")
		w.moveDevice(observatory.WeatherStationKind, "lab", "telescope", "east")
		w.indi.refuse("Telescope Simulator", "CONNECTION")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationFailed, 15*time.Minute)
		w.indi.accept("Telescope Simulator", "CONNECTION")
		w.retry("east-tonight")
		r := w.phase("east-tonight", observatory.ReservationReady, 15*time.Minute)

		if _, ok := r.Status.Session.Devices["WeatherStation/lab"]; ok {
			t.Errorf("east-tonight records the weather station on the telescope's server: %v", slices.Sorted(maps.Keys(r.Status.Session.Devices)))
		}
		pod, _ := w.api.object(podsCollection, "lab-weatherstation")
		if server := pod["metadata"].(map[string]any)["labels"].(map[string]any)[labelServer]; server != "lab-observatory" {
			t.Errorf("the weather station's pod runs on %v, want lab-observatory", server)
		}
	})
}
