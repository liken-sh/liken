package main

// A device that leaves a telescope or an observatory: while a
// reservation of it is Ready, its driver stops on the running server,
// and its pod, its Service, and its claim go. The other devices on the
// server stay connected.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func TestADeviceTakenOffAReadyTelescopeStopsOnTheRunningServer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.claimFocuser()
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		before, _ := w.api.object(podsCollection, "east-telescope")
		w.indi.setState("east-telescope", "CCD Simulator", "CCD_EXPOSURE", "Busy")

		w.put(observatory.FocuserKind, "east", map[string]any{"driver": map[string]any{"name": "indi_simulator_focus"}})
		w.until(time.Minute, "the focuser's pod stays", func() bool {
			return !slices.Contains(w.api.names(podsCollection), "east-focuser") && len(w.indi.connected("east-telescope")) == 11
		})
		time.Sleep(2 * statusWindow)
		synctest.Wait()

		if slices.Contains(w.api.names(servicesCollection), "east-focuser") || slices.Contains(w.api.names(claimsCollection), "east-focuser") {
			t.Errorf("services %v, claims %v", w.api.names(servicesCollection), w.api.names(claimsCollection))
		}
		after, _ := w.api.object(podsCollection, "east-telescope")
		if podUID(before) != podUID(after) || slices.Contains(links(after), "east-focuser") {
			t.Errorf("the server pod was replaced, or still names the focuser: links %v", links(after))
		}
		if got := w.indi.state("east-telescope", "CCD Simulator", "CCD_EXPOSURE"); got != "Busy" {
			t.Errorf("the camera's exposure is %q, want the Busy exposure that ran before", got)
		}
		if r, _ := w.reservation("east-tonight"); r.Status.Phase != observatory.ReservationReady {
			t.Errorf("the reservation is %s", r.Status.Phase)
		}
		focuser, _ := decode[observatory.Focuser](t, w.api, kindCollection(observatory.FocuserKind), "east")
		if focuser.Status.Phase != observatory.DeviceInventory {
			t.Errorf("focuser = %+v", focuser.Status)
		}

		wantFocuser := "Normal PodDeleted: Deleted pod east-focuser, because the Focuser left Telescope east while Reservation east-tonight is Ready."
		if got := typedEvents(w.api, observatory.FocuserKind, "east"); !slices.Contains(got, wantFocuser) {
			t.Errorf("focuser Events = %q, want %q", got, wantFocuser)
		}
		wantTelescope := "Normal DriverStopped: Stopped the driver of Focuser east on server east-telescope while Reservation east-tonight is Ready. The server did not restart."
		got := typedEvents(w.api, observatory.TelescopeKind, "east")
		if !slices.Contains(got, wantTelescope) || slices.ContainsFunc(got, func(e string) bool { return strings.HasPrefix(e, "Warning") }) {
			t.Errorf("telescope Events = %q, want %q and no Warning", got, wantTelescope)
		}
	})
}

// A device that leaves the observatory stops on the observatory's
// server, and the dome on that server stays connected.
func TestADeviceTakenOffTheObservatoryStopsOnItsRunningServer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		site := serverRef{observatory.ObservatoryKind, "lab"}.String()
		before, _ := w.api.object(podsCollection, site)
		connected := len(w.indi.connected(site))

		w.put(observatory.SkyQualityMeterKind, "lab", map[string]any{"driver": map[string]any{"name": "indi_simulator_sqm"}})
		w.until(time.Minute, "the meter stays on the observatory's server", func() bool {
			return !slices.Contains(w.api.names(podsCollection), "lab-skyqualitymeter") && len(w.indi.connected(site)) == connected-1
		})

		after, _ := w.api.object(podsCollection, site)
		if podUID(before) != podUID(after) {
			t.Error("the observatory's server pod was replaced")
		}
		if !slices.Contains(w.indi.connected(site), "Dome Simulator") {
			t.Errorf("connected = %q, want the dome", w.indi.connected(site))
		}
		want := "DriverStopped: Stopped the driver of SkyQualityMeter lab on server " + site + " while Reservation east-tonight is Ready. The server did not restart."
		if got := eventsAbout(w.api, observatory.ObservatoryKind, "lab", reasonDriverStopped); !slices.Equal(got, []string{want}) {
			t.Errorf("Events = %q, want %q", got, want)
		}
	})
}

// A device that leaves the telescope of a reservation that is not
// active changes nothing that runs, and posts no Warning.
func TestADeviceMovedOffAnIdleTelescopeIsAnEdit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.put(observatory.CameraKind, "west-main", map[string]any{"driver": map[string]any{"name": "indi_simulator_ccd"}})
		w.until(time.Minute, "west-main is not on the shelf", func() bool {
			c, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "west-main")
			return c.Status.Phase == observatory.DeviceInventory
		})
		w.put(observatory.CameraKind, "west-main", map[string]any{"opticalTrain": "west-imaging", "driver": map[string]any{"name": "indi_simulator_ccd"}})
		w.until(time.Minute, "west-main is not Idle", func() bool {
			c, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "west-main")
			return c.Status.Phase == observatory.DeviceIdle
		})
		if pods := w.api.names(podsCollection); len(pods) != 0 {
			t.Errorf("pods = %v", pods)
		}
		events := append(typedEvents(w.api, observatory.CameraKind, "west-main"), typedEvents(w.api, observatory.TelescopeKind, "west")...)
		if slices.ContainsFunc(events, func(e string) bool { return strings.HasPrefix(e, "Warning") }) {
			t.Errorf("Events = %q, want no Warning", events)
		}
	})
}
