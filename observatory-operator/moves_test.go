package main

// A device that leaves a telescope while a reservation of it is Ready:
// its pod, its Service, and its claim go, the server restarts without
// it, and the Telescope and the device each post an Event.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func TestADeviceTakenOffAReadyTelescopeLeavesNoPodOrClaim(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.claimFocuser()
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		before, _ := w.api.object(podsCollection, "east-telescope")

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
		if podUID(before) == podUID(after) || slices.Contains(links(after), "east-focuser") {
			t.Errorf("the server was not replaced without the focuser: links %v", links(after))
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
		wantTelescope := "Warning ServerReplaced: Replaced pod east-telescope while Reservation east-tonight is Ready, because its devices changed. " +
			"Every device on it disconnected, and an exposure in progress ended. The runner connects each device again."
		if got := typedEvents(w.api, observatory.TelescopeKind, "east"); !slices.Contains(got, wantTelescope) {
			t.Errorf("telescope Events = %q, want %q", got, wantTelescope)
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
