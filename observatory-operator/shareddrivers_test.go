package main

// Two devices on one server that name the same driver. INDI names a
// device after its model, so both drivers would define one device, and
// the second driver would break the first. One device runs, and the
// other never starts and reports Error.

import (
	"slices"
	"strings"
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
		running string
		winner  string
		loser   string
	}{
		{name: "the running device, though the other is older", devices: []*device{focuserOn("aaa", early), focuserOn("east", late)}, running: "east-focuser:7625\n", winner: "east", loser: "aaa"},
		{name: "the older device, while neither runs", devices: []*device{focuserOn("aaa", late), focuserOn("east", early)}, winner: "east", loser: "aaa"},
		{name: "the first name, between devices of one age", devices: []*device{focuserOn("spare", early), focuserOn("east", early)}, winner: "east", loser: "spare"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tr := &tree{devices: c.devices, pods: map[string]*pod{
				"east-telescope": {Metadata: meta{Name: "east-telescope", Annotations: map[string]string{annotationDrivers: c.running}}},
			}}
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

func TestASecondDeviceWithTheSameDriverNeverStarts(t *testing.T) {
	t.Parallel()
	installed := map[string]any{"opticalTrain": "east-guiding", "driver": map[string]any{"name": "indi_simulator_focus"}}
	cases := []struct {
		name   string
		second string
		// setUp brings the world to the moment the second focuser
		// exists beside Focuser east, with the reservation Ready.
		setUp func(w *world, second string)
	}{
		{name: "it joins a Ready telescope", second: "spare", setUp: func(w *world, second string) {
			w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
			w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
			w.put(observatory.FocuserKind, second, installed)
		}},
		{name: "it is there at activation, newer, with an earlier name", second: "aaa", setUp: func(w *world, second string) {
			time.Sleep(time.Minute)
			w.put(observatory.FocuserKind, second, installed)
			w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
			w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				c.setUp(w, c.second)
				w.until(time.Minute, "the second focuser is not in Error", func() bool {
					f, _ := decode[observatory.Focuser](t, w.api, kindCollection(observatory.FocuserKind), c.second)
					return f.Status.Phase == observatory.DeviceError
				})
				time.Sleep(2 * statusWindow)
				synctest.Wait()

				second, _ := decode[observatory.Focuser](t, w.api, kindCollection(observatory.FocuserKind), c.second)
				want := "Failed: Focuser east runs driver indi_simulator_focus on server east-telescope already"
				if ready := second.Status.Conditions[1]; !strings.HasPrefix(ready.Message, want) {
					t.Errorf("Ready = %+v, want a message that begins %q", ready, want)
				}
				first, _ := decode[observatory.Focuser](t, w.api, kindCollection(observatory.FocuserKind), "east")
				if first.Status.Phase != observatory.DeviceConnected {
					t.Errorf("Focuser east = %+v", first.Status)
				}
				server, _ := w.api.object(podsCollection, "east-telescope")
				if slices.Contains(w.api.names(podsCollection), c.second+"-focuser") || slices.Contains(links(server), c.second+"-focuser") {
					t.Errorf("pods %v, drivers %v", w.api.names(podsCollection), links(server))
				}
				if r, _ := w.reservation("east-tonight"); r.Status.Phase != observatory.ReservationReady {
					t.Errorf("the reservation is %s", r.Status.Phase)
				}

				w.put(observatory.FocuserKind, c.second, map[string]any{"driver": map[string]any{"name": "indi_simulator_focus"}})
				w.until(time.Minute, "the second focuser is not on the shelf", func() bool {
					f, _ := decode[observatory.Focuser](t, w.api, kindCollection(observatory.FocuserKind), c.second)
					return f.Status.Phase == observatory.DeviceInventory
				})
				time.Sleep(2 * statusWindow)
				synctest.Wait()
				if first, _ := decode[observatory.Focuser](t, w.api, kindCollection(observatory.FocuserKind), "east"); first.Status.Phase != observatory.DeviceConnected {
					t.Errorf("after the second focuser left, Focuser east = %+v", first.Status)
				}
			})
		})
	}
}
