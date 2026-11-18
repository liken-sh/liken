package main

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

func slews(w *runnerWorld) int {
	return len(slices.DeleteFunc(w.indi.Changes(), func(c string) bool { return !isSlew(c) }))
}

func TestAParkedMountIsUnparked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.indi.Report(mountINDI, "TELESCOPE_PARK", "PARK", "On")
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		if got := member(w.indi.Changes(), mountINDI+".TELESCOPE_PARK", "UNPARK"); got != "On" {
			t.Errorf("the UNPARK switch is %q, want On", got)
		}
		if got := w.eventReasons("Reservation", tonight); !slices.Contains(got, reasonUnparked) {
			t.Errorf("the Event reasons on the Reservation are %q, want %s among them", got, reasonUnparked)
		}
	})
}

func TestAMountThatReportsTrackingAsBusyIsTracking(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.indi.Move(mountINDI, "TELESCOPE_TRACK_STATE")
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		if slews(w) != 1 {
			t.Errorf("the mount slewed %d times, want 1", slews(w))
		}
	})
}

// A target that the runner cannot slew to fails with the reason
// SlewFailed and a Warning Event, and the runner does not try it again
// for the rest of the Reservation.
func TestATargetTheMountCannotReachFails(t *testing.T) {
	cases := []struct {
		name   string
		change func(*runnerWorld, *astrophotography.Target, *observatory.Reservation)
	}{
		{"a mount that refuses the slew", func(w *runnerWorld, _ *astrophotography.Target, _ *observatory.Reservation) {
			w.indi.Refuse(mountINDI, "EQUATORIAL_EOD_COORD")
		}},
		{"a mount that refuses to unpark", func(w *runnerWorld, _ *astrophotography.Target, _ *observatory.Reservation) {
			w.indi.Report(mountINDI, "TELESCOPE_PARK", "PARK", "On")
			w.indi.Refuse(mountINDI, "TELESCOPE_PARK")
		}},
		{"a mount that refuses to track", func(w *runnerWorld, _ *astrophotography.Target, _ *observatory.Reservation) {
			w.indi.Refuse(mountINDI, "TELESCOPE_TRACK_STATE")
		}},
		{"a mount that never stops moving", func(w *runnerWorld, _ *astrophotography.Target, _ *observatory.Reservation) {
			w.indi.Move(mountINDI, "EQUATORIAL_EOD_COORD")
			w.indi.SetState(mountINDI, "EQUATORIAL_EOD_COORD", "Busy")
		}},
		{"a mount whose driver is not connected", func(w *runnerWorld, _ *astrophotography.Target, _ *observatory.Reservation) {
			w.disconnect(mountINDI)
		}},
		{"a session with no mount", func(_ *runnerWorld, _ *astrophotography.Target, r *observatory.Reservation) {
			delete(r.Status.Session.Devices, "Mount/east-mount")
		}},
		{"a right ascension that does not read", func(_ *runnerWorld, x *astrophotography.Target, _ *observatory.Reservation) {
			x.Spec.Coordinates.RA = "25:00:00"
		}},
		{"a declination that does not read", func(_ *runnerWorld, x *astrophotography.Target, _ *observatory.Reservation) {
			x.Spec.Coordinates.Dec = "north"
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				x, res := target("m42", lights(1)), reservationIn(observatory.ReservationReady)
				c.change(w, x, res)
				w.put(targetsPath, x)
				w.put(reservationsPath, res)
				w.start(t)
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SlewFailed" })
				before := slews(w)
				time.Sleep(time.Hour)
				synctest.Wait()
				if slews(w) != before {
					t.Errorf("the mount slewed %d times after the target failed, want %d", slews(w), before)
				}
				posted := w.runnerEvents("Target", "m42")
				if last := posted[len(posted)-1]; last.Type != "Warning" {
					t.Errorf("the last Event is %s %s %q, want a Warning", last.Type, last.Reason, last.Message)
				}
			})
		})
	}
}

// A mount that is already slewing when the runner starts, such as after
// a restart during a slew, finishes that slew first. The runner sends
// its own slew only after that, and then waits for its own slew to
// end.
func TestTheRunnerWaitsForTheMountToStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.indi.Move(mountINDI, "EQUATORIAL_EOD_COORD")
		w.indi.SetState(mountINDI, "EQUATORIAL_EOD_COORD", "Busy")
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
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

func TestNewCoordinatesSlewAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(2)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		moved := w.targetNow(t, "m42")
		moved.Spec.Coordinates.RA = "05:36:00"
		moved.Metadata.Generation = 2
		w.put(targetsPath, moved)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		if got := slews(w); got != 2 {
			t.Errorf("the mount slewed %d times, want 2", got)
		}
	})
}

// When the Reservation ends during the slew or while the mount
// settles, the target goes back to Waiting, not to SlewFailed.
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
				w.put(reservationsPath, reservationIn(observatory.ReservationReady))
				w.start(t)
				waitUntil(t, func() bool { return slews(w) == 1 })
				w.put(reservationsPath, reservationIn(observatory.ReservationDeactivating))
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False Waiting" })
			})
		})
	}
}

// When the API server refuses the Imaging status write of a second
// slew, the pass ends, and the next pass claims the target and slews
// again.
func TestASecondSlewThatCannotBeRecordedIsTriedAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(2)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
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
			t.Error("the runner did not slew again after the coordinates changed")
		}
	})
}
