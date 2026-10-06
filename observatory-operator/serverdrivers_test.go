package main

// A device that joins a telescope while a reservation of it is Ready:
// it gets its pod, its driver starts on the running server, and the
// operator connects it. The other devices on the server stay
// connected, and an exposure in progress runs on.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func TestADeviceAddedWhileReadyStartsOnTheRunningServer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		before, _ := w.api.object(podsCollection, "east-telescope")
		w.indi.setState("east-telescope", "CCD Simulator", "CCD_EXPOSURE", "Busy")

		w.put(observatory.SkyQualityMeterKind, "east", map[string]any{"telescope": "east", "driver": map[string]any{"name": "indi_simulator_sqm"}})
		w.until(time.Minute, "the new device is not connected", func() bool {
			return slices.Contains(w.indi.connected("east-telescope"), "SQM Simulator") && len(w.indi.connected("east-telescope")) == 13
		})

		after, _ := w.api.object(podsCollection, "east-telescope")
		if podUID(before) != podUID(after) {
			t.Error("the server pod was replaced")
		}
		if !slices.Contains(links(after), "east-skyqualitymeter") {
			t.Errorf("the server's drivers are %v", links(after))
		}
		if got := w.indi.state("east-telescope", "CCD Simulator", "CCD_EXPOSURE"); got != "Busy" {
			t.Errorf("the camera's exposure is %q, want the Busy exposure that ran before", got)
		}
		want := "Normal DriverStarted: Started the driver of SkyQualityMeter east on server east-telescope while Reservation east-tonight is Ready. The server did not restart."
		got := typedEvents(w.api, observatory.TelescopeKind, "east")
		if !slices.Contains(got, want) || slices.ContainsFunc(got, func(e string) bool { return strings.HasPrefix(e, "Warning") }) {
			t.Errorf("telescope Events = %q, want %q and no Warning", got, want)
		}
		if got := eventsAbout(w.api, observatory.TelescopeKind, "east", reasonDriverStarted); len(got) != 1 {
			t.Errorf("DriverStarted Events = %q, want one", got)
		}
		if r, _ := w.reservation("east-tonight"); r.Status.Phase != observatory.ReservationReady {
			t.Errorf("the reservation is %s", r.Status.Phase)
		}
	})
}

// A server pod that is gone while Ready, such as one a node's eviction
// deleted, is created again with every device. The new pod lists the
// devices itself, so the operator starts no driver on it.
func TestAServerCreatedAgainStartsWithEveryDevice(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		before, _ := w.api.object(podsCollection, "east-telescope")
		w.api.deleteNamed(podsCollection, "east-telescope")
		w.until(time.Minute, "the server is not back", func() bool {
			p, ok := w.api.object(podsCollection, "east-telescope")
			return ok && podUID(p) != podUID(before) && len(w.indi.connected("east-telescope")) == 12
		})
		after, _ := w.api.object(podsCollection, "east-telescope")
		if !slices.Equal(links(after), links(before)) {
			t.Errorf("drivers = %v, want %v", links(after), links(before))
		}
		if got := eventsAbout(w.api, observatory.TelescopeKind, "east", reasonDriverStarted); len(got) != 0 {
			t.Errorf("DriverStarted Events = %q, want none", got)
		}
	})
}
