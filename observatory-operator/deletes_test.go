package main

// A device that a person deletes while a reservation of its telescope
// or observatory is Ready: its driver stops on the running server, and
// its pod, its Service, and its claim go. The other devices on the
// server stay connected. devicedelete_test.go holds its deactivation
// and its finalizer.

import (
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func TestADeviceDeletedWhileReadyStopsOnTheRunningServer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.claimFocuser()
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		before, _ := w.api.object(podsCollection, "east-telescope")
		w.indi.setState("east-telescope", "CCD Simulator", "CCD_EXPOSURE", "Busy")

		w.api.deleteNamed(kindCollection(observatory.FocuserKind), "east")
		w.until(time.Minute, "the focuser's pod stays", func() bool {
			return !slices.Contains(w.api.names(podsCollection), "east-focuser") && len(w.indi.connected("east-telescope")) == 10
		})
		w.gone(observatory.FocuserKind, "east", time.Minute)
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
		wantFocuser := "Normal PodDeleted: Deleted pod east-focuser, because the Focuser was deleted while Reservation east-tonight is Ready."
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

// A deleted device's pod goes only after the server reports its driver
// stopped. A pod that went first would end the driver's connection,
// and indiserver would start the driver again. A shim that never stops
// the driver holds the pod for driverStopLimit, and no longer.
func TestADeletedDevicesPodGoesAfterItsDriverStops(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		shim     time.Duration
		bounded  bool
		restarts []string
	}{
		{name: "a shim that stops the driver in a second", shim: time.Second},
		{name: "a shim that never stops the driver", shim: time.Hour, bounded: true, restarts: []string{"east-telescope east-focuser"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := readyWorld(t)
				w.indi.slowShim(c.shim)
				start := time.Now()

				w.api.deleteNamed(kindCollection(observatory.FocuserKind), "east")
				w.until(5*time.Minute, "the focuser's pod stays", func() bool {
					return !slices.Contains(w.api.names(podsCollection), "east-focuser")
				})

				if got := w.indi.restarted(); !slices.Equal(got, c.restarts) {
					t.Errorf("restarted drivers = %q, want %q", got, c.restarts)
				}
				if waited := time.Since(start); (waited >= driverStopLimit) != c.bounded {
					t.Errorf("the pod went after %v; the bound is %v", waited, driverStopLimit)
				}
			})
		})
	}
}

// A release stops each driver on its running server before it deletes
// the driver's pod, as a deleted device does. A server that stops
// with its devices loses their pods first, so indiserver can start each
// of their shims once more, against a device pod that is gone, while
// its own pod stops. Whether it does depends on which delete the server
// sees first, so the test allows those restarts and no others.
func TestAReleaseStopsEachDriverBeforeItsPod(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.slowShim(time.Second)
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(10*time.Minute, "the Reservation is still there", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
		allowed := []string{"east-telescope east-switch", "lab-observatory lab-dome", "lab-observatory lab-weatherstation"}
		if got := w.indi.restarted(); slices.ContainsFunc(got, func(r string) bool { return !slices.Contains(allowed, r) }) {
			t.Errorf("restarted drivers = %q, want only the drivers of a stopping server, %q", got, allowed)
		}
	})
}

// A device of the observatory that a person deletes stops on the
// observatory's server, and the dome on that server stays connected.
func TestADeviceDeletedFromTheObservatoryStopsOnItsRunningServer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.put(observatory.ReceiverKind, "lab", map[string]any{"observatory": "lab", "driver": map[string]any{"name": "indi_simulator_receiver"}})
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		site := serverRef{observatory.ObservatoryKind, "lab"}.String()
		connected := len(w.indi.connected(site))
		before, _ := w.api.object(podsCollection, site)

		w.api.deleteNamed(kindCollection(observatory.ReceiverKind), "lab")
		w.until(time.Minute, "the receiver stays on the observatory's server", func() bool {
			return !slices.Contains(w.api.names(podsCollection), "lab-receiver") && len(w.indi.connected(site)) == connected-1
		})

		after, _ := w.api.object(podsCollection, site)
		if podUID(before) != podUID(after) {
			t.Error("the observatory's server pod was replaced")
		}
		if !slices.Contains(w.indi.connected(site), "Dome Simulator") {
			t.Errorf("connected = %q, want the dome", w.indi.connected(site))
		}
		want := "DriverStopped: Stopped the driver of Receiver lab on server " + site + " while Reservation east-tonight is Ready. The server did not restart."
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

// The Event on a deleted device says that its output went off, and an
// output that a device on the server still names stays on: the
// focuser and the rotator share output 2.
func TestADeletedDeviceSwitchesOffOnlyAnOutputNoDeviceStillNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		kind   observatory.Kind
		device string
		event  string
		output string
		sent   bool
	}{
		{"a camera alone on output 1", observatory.CameraKind, "east-main",
			"Normal PodDeleted: Deleted pod east-main-camera, because the Camera was deleted while Reservation east-tonight is Ready. Switched off output 1 of Switch east.",
			outputsOff[0], true},
		{"a focuser that shares output 2", observatory.FocuserKind, "east",
			"Normal PodDeleted: Deleted pod east-focuser, because the Focuser was deleted while Reservation east-tonight is Ready.",
			outputsOff[1], false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := readyWorld(t)
				w.api.deleteNamed(kindCollection(c.kind), c.device)
				w.until(5*time.Minute, "no PodDeleted Event", func() bool {
					return slices.Contains(typedEvents(w.api, c.kind, c.device), c.event)
				})
				if sent := slices.Contains(w.indi.changes(), c.output); sent != c.sent {
					t.Errorf("sent %q = %t, want %t", c.output, sent, c.sent)
				}
			})
		})
	}
}

// A device deleted together with its reservation loses its pod in the
// StopDevices step, with the other devices of its server, and before
// the PowerOff step switches any output off. A pod that outlived its
// driver's link would see its socat exit and the kubelet start it
// again. The PowerOff step still switches off the output of a deleted
// camera that is alone on output 1, from the session's record.
func TestADeviceDeletedWithItsReservationStopsWithTheOtherPods(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		deletes []named
		pods    []string
	}{
		{"a dust cap with its train",
			[]named{{observatory.OpticalTrainKind, "east-imaging"}, {observatory.DustCapKind, "east"}},
			[]string{"east-dustcap", "east-main-camera", "east-mount"}},
		{"a dust cap and a camera with their train",
			[]named{{observatory.OpticalTrainKind, "east-imaging"}, {observatory.DustCapKind, "east"}, {observatory.CameraKind, "east-main"}},
			[]string{"east-dustcap", "east-main-camera", "east-mount"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				readyOf(w)
				var mu sync.Mutex
				late := map[string]bool{}
				w.api.hear(func(r *http.Request) {
					pod, ok := strings.CutPrefix(r.URL.Path, podsCollection+"/")
					if r.Method != http.MethodDelete || !ok {
						return
					}
					off := slices.ContainsFunc(w.indi.changes(), func(change string) bool { return slices.Contains(outputsOff, change) })
					mu.Lock()
					defer mu.Unlock()
					late[pod] = off
				})
				w.deleteDuringAbort(c.deletes...)
				w.gone(observatory.ReservationKind, "east-tonight", 10*time.Minute)

				mu.Lock()
				defer mu.Unlock()
				for _, pod := range c.pods {
					if off, deleted := late[pod]; !deleted || off {
						t.Errorf("pod %s deleted = %t, after an output went off = %t; want deleted before PowerOff", pod, deleted, off)
					}
				}
				for _, change := range outputsOff {
					if !slices.Contains(w.indi.changes(), change) {
						t.Errorf("changes = %q\nwant %q", w.indi.changes(), change)
					}
				}
			})
		})
	}
}
