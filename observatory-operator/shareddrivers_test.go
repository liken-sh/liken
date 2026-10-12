package main

// Two devices on one server that name the same driver. INDI names a
// device after its model, so both drivers would define one device, and
// the second driver would break the first. At activation, such a pair
// fails the step that starts the server, and a device placed beside a
// running one during a session waits for the next session.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// focuserOn builds a Focuser on the east telescope that runs the focuser
// simulator.
func focuserOn(name string, created time.Time) *device {
	d := &device{kind: observatory.FocuserKind}
	d.object.Metadata.Name = name
	d.object.Metadata.CreationTimestamp = &created
	d.object.Spec.Telescope = "east"
	d.object.Spec.Driver.Name = "indi_simulator_focus"
	return d
}

// takenBy answers the name of the device that runs a device's driver
// in its place, or "".
func takenBy(t *tree, d *device) string {
	if holder, ok := t.driverTakenBy(d); ok {
		return holder.name()
	}
	return ""
}

func TestOneOfTwoDevicesWithTheSameDriverRuns(t *testing.T) {
	t.Parallel()
	early := time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC)
	late := early.Add(time.Minute)
	cases := []struct {
		name    string
		devices []*device
		winner  string
		loser   string
	}{
		{name: "the older device", devices: []*device{focuserOn("aaa", late), focuserOn("east", early)}, winner: "east", loser: "aaa"},
		{name: "the first name, between devices of one age", devices: []*device{focuserOn("spare", early), focuserOn("east", early)}, winner: "east", loser: "spare"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tr := &tree{devices: c.devices, pods: map[string]*pod{}}
			on := tr.devicesOn(serverRef{observatory.TelescopeKind, "east"})
			if len(on) != 1 || on[0].name() != c.winner {
				t.Errorf("devicesOn = %q, want %s alone", names(on), c.winner)
			}
			loser, _ := tr.device(observatory.FocuserKind, c.loser)
			winner, _ := tr.device(observatory.FocuserKind, c.winner)
			if got := takenBy(tr, loser); got != c.winner {
				t.Errorf("the driver of %s is taken by %q, want %s", c.loser, got, c.winner)
			}
			if got := takenBy(tr, winner); got != "" {
				t.Errorf("the driver of %s is taken by %q", c.winner, got)
			}
		})
	}
}

// A device placed beside a running device with the same driver during
// a session never starts, and the running device stays connected, also
// after the second device goes back to the shelf. At activation, the
// same pair fails the step instead (failures_test.go).
func TestASecondDeviceWithTheSameDriverNeverStarts(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.put(observatory.FocuserKind, "spare", map[string]any{"opticalTrain": "east-guiding", "driver": map[string]any{"name": "indi_simulator_focus"}})
		w.until(time.Minute, "the second focuser is not Idle", func() bool {
			f, _ := decode[observatory.Focuser](t, w.api, kindCollection(observatory.FocuserKind), "spare")
			return f.Status.Phase == observatory.DeviceIdle
		})
		w.settle()

		first, _ := decode[observatory.Focuser](t, w.api, kindCollection(observatory.FocuserKind), "east")
		if first.Status.Phase != observatory.DeviceConnected {
			t.Errorf("Focuser east = %+v", first.Status)
		}
		server, _ := w.api.object(podsCollection, "east-telescope")
		if slices.Contains(w.api.names(podsCollection), "spare-focuser") || slices.Contains(links(server), "spare-focuser") {
			t.Errorf("pods %v, drivers %v", w.api.names(podsCollection), links(server))
		}

		w.put(observatory.FocuserKind, "spare", map[string]any{"driver": map[string]any{"name": "indi_simulator_focus"}})
		w.until(time.Minute, "the second focuser is not on the shelf", func() bool {
			f, _ := decode[observatory.Focuser](t, w.api, kindCollection(observatory.FocuserKind), "spare")
			return f.Status.Phase == observatory.DeviceInventory
		})
		w.settle()
		if first, _ := decode[observatory.Focuser](t, w.api, kindCollection(observatory.FocuserKind), "east"); first.Status.Phase != observatory.DeviceConnected {
			t.Errorf("after the second focuser left, Focuser east = %+v", first.Status)
		}
	})
}
