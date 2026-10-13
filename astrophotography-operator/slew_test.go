package main

// The mount: the slew to J2000 coordinates at the equinox of the date,
// the unpark and the tracking it needs first, a slew that a run before
// a restart started, a mount that refuses or never stops, and new
// coordinates during the night.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func isSlew(change string) bool { return strings.HasPrefix(change, mountINDI+".EQUATORIAL_EOD_COORD") }

func slews(w *night) int {
	return len(slices.DeleteFunc(w.indi.Changes(), func(c string) bool { return !isSlew(c) }))
}

// A parked mount is unparked before the slew, with an Event on the
// reservation.
func TestAParkedMountIsUnparked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.indi.Report(mountINDI, "TELESCOPE_PARK", "PARK", "On")
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		if got := member(w.indi.Changes(), mountINDI+".TELESCOPE_PARK", "UNPARK"); got != "On" {
			t.Errorf("UNPARK %q, want On", got)
		}
		if got := w.eventReasons("Reservation", tonight); !slices.Contains(got, reasonUnparked) {
			t.Errorf("Events on the reservation %q, want %s", got, reasonUnparked)
		}
	})
}

// A target that the runner cannot slew to fails with SlewFailed, a
// Warning, and the runner leaves it for the rest of the reservation.
func TestATargetTheMountCannotReachFails(t *testing.T) {
	cases := []struct {
		name   string
		change func(*night, *astrophotography.Target, *observatory.Reservation)
	}{
		{"a mount that refuses the slew", func(w *night, _ *astrophotography.Target, _ *observatory.Reservation) {
			w.indi.Refuse(mountINDI, "EQUATORIAL_EOD_COORD")
		}},
		{"a mount that refuses to unpark", func(w *night, _ *astrophotography.Target, _ *observatory.Reservation) {
			w.indi.Report(mountINDI, "TELESCOPE_PARK", "PARK", "On")
			w.indi.Refuse(mountINDI, "TELESCOPE_PARK")
		}},
		{"a mount that refuses to track", func(w *night, _ *astrophotography.Target, _ *observatory.Reservation) {
			w.indi.Refuse(mountINDI, "TELESCOPE_TRACK_STATE")
		}},
		{"a mount that never stops moving", func(w *night, _ *astrophotography.Target, _ *observatory.Reservation) {
			w.indi.Move(mountINDI, "EQUATORIAL_EOD_COORD")
			w.indi.SetState(mountINDI, "EQUATORIAL_EOD_COORD", "Busy")
		}},
		{"a mount whose driver is not connected", func(w *night, _ *astrophotography.Target, _ *observatory.Reservation) {
			w.disconnect(mountINDI)
		}},
		{"a session with no mount", func(_ *night, _ *astrophotography.Target, r *observatory.Reservation) {
			delete(r.Status.Session.Devices, "Mount/east-mount")
		}},
		{"a right ascension that does not read", func(_ *night, x *astrophotography.Target, _ *observatory.Reservation) {
			x.Spec.Coordinates.RA = "25:00:00"
		}},
		{"a declination that does not read", func(_ *night, x *astrophotography.Target, _ *observatory.Reservation) {
			x.Spec.Coordinates.Dec = "north"
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				x, res := target("m42", lights(1)), readyOrNot(observatory.ReservationReady)
				c.change(w, x, res)
				w.put(targetsPath, x)
				w.put(reservationsPath, res)
				w.start(t)
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SlewFailed" })
				before := slews(w)
				time.Sleep(time.Hour)
				synctest.Wait()
				if slews(w) != before {
					t.Error("the runner slewed again to a target that failed")
				}
				events := w.api.recorded.About("Target", "m42")
				if last := events[len(events)-1]; last.Type != "Warning" {
					t.Errorf("the last Event is %s %s %q, want a Warning", last.Type, last.Reason, last.Message)
				}
			})
		})
	}
}

// A mount that still slews when the runner starts, such as after a
// restart during a slew, finishes that slew before the runner sends its
// own, and the runner waits for its own slew to end.
func TestTheRunnerWaitsForTheMountToStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.indi.Move(mountINDI, "EQUATORIAL_EOD_COORD")
		w.indi.SetState(mountINDI, "EQUATORIAL_EOD_COORD", "Busy")
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		time.Sleep(time.Minute)
		synctest.Wait()
		if slews(w) != 0 {
			t.Fatal("the runner slewed while the mount moved")
		}
		w.indi.SetState(mountINDI, "EQUATORIAL_EOD_COORD", "Ok")
		waitUntil(t, func() bool { return slews(w) == 1 })
		time.Sleep(time.Minute)
		synctest.Wait()
		if w.targetNow(t, "m42").Status.Exposure != nil {
			t.Fatal("the runner exposed while its slew ran")
		}
		w.indi.SetState(mountINDI, "EQUATORIAL_EOD_COORD", "Ok")
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
	})
}

// A person edits the coordinates during the night, and the runner slews
// again before the next frame.
func TestNewCoordinatesSlewAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(2)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		moved := w.targetNow(t, "m42")
		moved.Spec.Coordinates.RA = "05:36:00"
		moved.Metadata.Generation = 2
		w.put(targetsPath, moved)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		if got := slews(w); got != 2 {
			t.Errorf("%d slews, want 2", got)
		}
	})
}

// The reservation ends during the slew or while the mount settles: the
// runner stops, and the target waits with no verdict on the mount.
func TestAReservationThatEndsDuringTheSlew(t *testing.T) {
	cases := []struct {
		name string
		// moves makes the slew last until the reservation ends.
		moves bool
	}{
		{"during the slew", true},
		{"while the mount settles", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				if c.moves {
					w.indi.Move(mountINDI, "EQUATORIAL_EOD_COORD")
				}
				w.put(targetsPath, target("m42", lights(1)))
				w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
				w.start(t)
				waitUntil(t, func() bool { return slews(w) == 1 })
				w.put(reservationsPath, readyOrNot(observatory.ReservationDeactivating))
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False Waiting" })
			})
		})
	}
}

// A second slew whose Imaging condition the API server refuses to
// record ends the pass, and the next pass claims the target and slews
// again.
func TestASecondSlewThatCannotBeRecordedIsTriedAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(2)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		refused := false
		w.api.beforeStatus = func(_, _ string, body map[string]any) bool {
			if refused || !strings.Contains(imagingIn(body), "slews to") {
				return false
			}
			refused = true
			return true
		}
		moved := w.targetNow(t, "m42")
		moved.Spec.Coordinates.RA = "05:36:00"
		moved.Metadata.Generation = 2
		w.put(targetsPath, moved)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		if !refused {
			t.Error("the runner never slewed again")
		}
	})
}
