package main

// The named states in a device's readings: one field that names the
// state a person reads, from the switches and lights the driver
// reports.

import (
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// reading answers one string field of a device's status.readings, or
// "" when the field is absent.
func (w *world) reading(kind observatory.Kind, name, field string) string {
	w.t.Helper()
	object, _ := decode[map[string]any](w.t, w.api, kindCollection(kind), name)
	status, _ := object["status"].(map[string]any)
	readings, _ := status["readings"].(map[string]any)
	value, _ := readings[field].(string)
	return value
}

// stateStep is one thing a driver or a client does, and the named
// state that the device reads after it.
type stateStep struct {
	name string
	do   func(w *world)
	want string
}

// walk runs each step in order, and checks one reading after each.
func (w *world) walk(kind observatory.Kind, name, field string, steps []stateStep) {
	w.t.Helper()
	for _, s := range steps {
		s.do(w)
		w.settle()
		if got := w.reading(kind, name, field); got != s.want {
			w.t.Errorf("%s: %s %s readings.%s = %q, want %q", s.name, kind.Name, name, field, got, s.want)
		}
	}
}

func TestAMountNamesItsState(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		const server, mount = "east-telescope", "Telescope Simulator"
		w.indi.move(mount, "TELESCOPE_PARK")
		w.walk(observatory.MountKind, "east", "state", []stateStep{
			{"unparked with tracking off", func(w *world) {}, string(observatory.MountStopped)},
			{"tracking on", func(w *world) {
				w.indi.ask(server, mount, "TELESCOPE_TRACK_STATE", "TRACK_ON=On", "TRACK_OFF=Off")
			}, string(observatory.MountTracking)},
			// A slew that ends in tracking reads Slewing while the
			// coordinates are Busy, then Tracking.
			{"a slew starts", func(w *world) {
				w.indi.setState(server, mount, "EQUATORIAL_EOD_COORD", "Busy")
			}, string(observatory.MountSlewing)},
			{"the slew ends", func(w *world) {
				w.indi.setState(server, mount, "EQUATORIAL_EOD_COORD", "Ok")
			}, string(observatory.MountTracking)},
			{"tracking off", func(w *world) {
				w.indi.ask(server, mount, "TELESCOPE_TRACK_STATE", "TRACK_ON=Off", "TRACK_OFF=On")
			}, string(observatory.MountStopped)},
			// A slew with tracking off still reads Slewing.
			{"a slew with tracking off", func(w *world) {
				w.indi.setState(server, mount, "EQUATORIAL_EOD_COORD", "Busy")
			}, string(observatory.MountSlewing)},
			// A park moves the mount too, and a Busy park comes first.
			{"a park starts during the slew", func(w *world) {
				w.indi.ask(server, mount, "TELESCOPE_PARK", "PARK=On", "UNPARK=Off")
			}, string(observatory.MountParking)},
			{"the park ends", func(w *world) {
				w.indi.setState(server, mount, "EQUATORIAL_EOD_COORD", "Ok")
				w.indi.setState(server, mount, "TELESCOPE_PARK", "Ok")
			}, string(observatory.MountParked)},
			{"an unpark starts", func(w *world) {
				w.indi.ask(server, mount, "TELESCOPE_PARK", "PARK=Off", "UNPARK=On")
			}, string(observatory.MountUnparking)},
			{"the unpark ends", func(w *world) {
				w.indi.setState(server, mount, "TELESCOPE_PARK", "Ok")
			}, string(observatory.MountStopped)},
			// libindi's telescope answers an abort during a park with
			// every park switch Off and the light Alert. The mount
			// stopped where it was, so it reads Stopped.
			{"a park starts again", func(w *world) {
				w.indi.ask(server, mount, "TELESCOPE_PARK", "PARK=On", "UNPARK=Off")
			}, string(observatory.MountParking)},
			{"a client aborts the park", func(w *world) {
				w.indi.ask(server, mount, "TELESCOPE_ABORT_MOTION", "ABORT=On")
			}, string(observatory.MountStopped)},
			{"the driver disconnects", func(w *world) {
				w.indi.ask(server, mount, "CONNECTION", "CONNECT=Off", "DISCONNECT=On")
			}, ""},
		})
		if got, want := w.stateOf(observatory.MountKind, "east", "Tracking"), ""; got != want {
			t.Errorf("Tracking = %q, want no such condition", got)
		}
	})
}

// A park switch that a driver leaves On after an abort, with the light
// Alert, does not say where the mount is, so the mount reads Stopped
// and not Parked.
func TestAMountWhoseParkFailedIsNotParked(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		const server, mount = "east-telescope", "Telescope Simulator"
		w.walk(observatory.MountKind, "east", "state", []stateStep{
			{"a park that fails", func(w *world) {
				w.indi.move(mount, "TELESCOPE_PARK")
				w.indi.ask(server, mount, "TELESCOPE_PARK", "PARK=On", "UNPARK=Off")
				w.indi.setState(server, mount, "TELESCOPE_PARK", "Alert")
			}, string(observatory.MountStopped)},
		})
	})
}

func TestADomeNamesItsPark(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		const server, dome = "lab-observatory", "Dome Simulator"
		// The dome does not park while the mount is unparked.
		w.indi.ask("east-telescope", "Telescope Simulator", "TELESCOPE_PARK", "PARK=On", "UNPARK=Off")
		w.relayedLast(server, "TELESCOPE_PARK", mountsParkedToLab)
		w.walk(observatory.DomeKind, "lab", "park", []stateStep{
			{"unparked", func(w *world) {}, string(observatory.DomeUnparked)},
			{"turning to its park position", func(w *world) {
				w.indi.setState(server, dome, "DOME_PARK", "Busy")
			}, string(observatory.DomeMoving)},
			{"parked", func(w *world) {
				w.indi.ask(server, dome, "DOME_PARK", "PARK=On", "UNPARK=Off")
			}, string(observatory.DomeParked)},
			{"the driver disconnects", func(w *world) {
				w.indi.ask(server, dome, "CONNECTION", "CONNECT=Off", "DISCONNECT=On")
			}, ""},
		})
	})
}

func TestACameraNamesItsExposure(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		const server, camera = "east-telescope", "CCD Simulator"
		exposure := func(state string) func(w *world) {
			return func(w *world) { w.indi.setState(server, camera, "CCD_EXPOSURE", state) }
		}
		w.walk(observatory.CameraKind, "east-main", "exposure", []stateStep{
			{"before the first exposure", func(w *world) {}, string(observatory.ExposureIdle)},
			{"an exposure runs", exposure("Busy"), string(observatory.ExposureExposing)},
			{"the exposure ends", exposure("Ok"), string(observatory.ExposureDone)},
			{"an exposure fails", exposure("Alert"), string(observatory.ExposureFailed)},
		})
	})
}

func TestAFlatPanelNamesItsLight(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		const server, panel = "east-telescope", "Light Panel Simulator"
		w.walk(observatory.FlatPanelKind, "east", "light", []stateStep{
			{"dark", func(w *world) {}, string(observatory.StateDark)},
			{"lit", func(w *world) {
				w.indi.ask(server, panel, "FLAT_LIGHT_CONTROL", "FLAT_LIGHT_ON=On", "FLAT_LIGHT_OFF=Off")
			}, string(observatory.StateLit)},
		})
	})
}

// lookup answers a lookup over a fixed set of INDI properties, so a
// test can name a state from a driver that defines only some of them.
func lookup(ps ...indi.Property) func(string) (indi.Property, bool) {
	return func(name string) (indi.Property, bool) {
		for _, p := range ps {
			if p.Name == name {
				return p, true
			}
		}
		return indi.Property{}, false
	}
}

func switchProperty(name string, state indi.State, on string) indi.Property {
	return indi.Property{Name: name, Type: indi.SwitchType, State: state, Members: []indi.Member{{Name: on, Switch: true}}}
}

func TestAMountWithoutParkOrTrackingNamesItsState(t *testing.T) {
	t.Parallel()
	coordinates := func(state indi.State) indi.Property {
		return indi.Property{Name: "EQUATORIAL_EOD_COORD", Type: indi.NumberType, State: state}
	}
	cases := []struct {
		name string
		with []indi.Property
		want observatory.MountState
	}{
		{"no coordinates", []indi.Property{switchProperty("TELESCOPE_PARK", indi.Ok, "PARK")}, ""},
		{"no park, tracking", []indi.Property{coordinates(indi.Ok), switchProperty("TELESCOPE_TRACK_STATE", indi.Ok, "TRACK_ON")}, observatory.MountTracking},
		{"no park, slewing", []indi.Property{coordinates(indi.Busy)}, observatory.MountSlewing},
		{"no tracking, parked", []indi.Property{coordinates(indi.Ok), switchProperty("TELESCOPE_PARK", indi.Ok, "PARK")}, observatory.MountParked},
		{"neither, still", []indi.Property{coordinates(indi.Ok)}, observatory.MountStopped},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mountState(lookup(c.with...)); got != c.want {
				t.Errorf("mountState = %q, want %q", got, c.want)
			}
		})
	}
}
