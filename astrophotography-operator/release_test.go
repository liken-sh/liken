package main

// The end of a reservation's claim of a target: the reservation ends,
// a person suspends or deletes the target, or the target cannot be
// imaged on this reservation.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The reservation ends during an exposure: observatory-operator's StopWork
// step stops the devices. The runner stops, and the target's claim and
// its frame in flight are cleared, with Imaging False and Waiting. The
// frame does not count, even when the driver wrote it: the promotion
// finds it in the outbox. A release that the API server refuses is
// tried again.
func TestAReservationThatEndsReleasesItsTarget(t *testing.T) {
	cases := []struct {
		name    string
		refused int
	}{
		{"the release lands", 0},
		{"the API server refuses the release once", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				w.put(targetsPath, target("m42", lights(3)))
				w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
				w.playCamera(t, saves)
				w.start(t)
				waitUntil(t, w.inFlight(t, "m42"))
				w.api.mu.Lock()
				w.api.failStatus = c.refused
				w.api.mu.Unlock()
				w.put(reservationsPath, readyOrNot(observatory.ReservationDeactivating))
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False Waiting" })
				waitUntil(t, func() bool { return len(w.worker.held()) == 1 })

				status := w.targetNow(t, "m42").Status
				if status.Reservation != "" || status.Exposure != nil {
					t.Errorf("reservation %q, exposure %+v; want both cleared", status.Reservation, status.Exposure)
				}
				if got := pendingOf(status); !slices.Equal(got, []int32{0}) {
					t.Errorf("pending %v, want [0]", got)
				}
				if got := w.eventReasons("Target", "m42"); got[len(got)-1] != astrophotography.ReasonWaiting {
					t.Errorf("Events on the target %q, want the last Waiting", got)
				}
			})
		})
	}
}

// A person suspends a target during the night: the runner finishes the
// frame in flight, and releases the target with Suspended. The target
// keeps its progress.
func TestASuspendedTargetIsReleasedAfterItsFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(3)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		suspended := w.targetNow(t, "m42")
		suspended.Spec.Suspend = true
		suspended.Metadata.Generation = 2
		w.put(targetsPath, suspended)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False Suspended" })
		status := w.targetNow(t, "m42").Status
		if status.Reservation != "" || !slices.Equal(pendingOf(status), []int32{1}) {
			t.Errorf("reservation %q, pending %v; want none and [1]", status.Reservation, pendingOf(status))
		}
	})
}

// A person deletes a target during an exposure, and the runner ends
// its work on it with no write.
func TestADeletedTargetEndsTheWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(3)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		w.api.remove(targetsPath, ns, "m42")
		time.Sleep(time.Hour)
		synctest.Wait()
		if got := len(w.worker.held()); got != 1 {
			t.Errorf("%d frames, want the one in flight", got)
		}
	})
}

// A person deletes a target during an exposure, and its finalizer holds
// the delete. The runner counts the frame in flight, releases the
// target with Deleting, and exposes no more of it. The target goes when
// promotion has moved the frame.
func TestADeletedTargetIsReleasedAfterItsFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(3)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		w.api.delete(targetsPath, ns, "m42")
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False "+astrophotography.ReasonDeleting })
		status := w.targetNow(t, "m42").Status
		if status.Reservation != "" || !slices.Equal(pendingOf(status), []int32{1}) {
			t.Errorf("reservation %q, pending %v; want none and [1]", status.Reservation, pendingOf(status))
		}

		// Promotion moves the frame, the way a report empties the outbox.
		promoted := w.targetNow(t, "m42")
		promoted.Status.Outboxes = nil
		w.put(targetsPath, promoted)
		waitUntil(t, func() bool { return w.api.get(targetsPath, ns, "m42") == nil })
		if got := len(w.worker.held()); got != 1 {
			t.Errorf("%d frames, want the one in flight", got)
		}
	})
}

// A target that the runner cannot image leaves the reservation with a
// reason, and the runner takes the next target.
func TestATargetThatCannotBeImagedIsReleased(t *testing.T) {
	cases := []struct {
		name   string
		change func(*astrophotography.Target, *observatory.Reservation)
		reason string
	}{
		{"a filter the wheel does not have", func(x *astrophotography.Target, _ *observatory.Reservation) {
			x.Spec.Sequences[0].Filter = "H_Beta"
		}, astrophotography.ReasonNoFilter},
		{"a filter on a train with no wheel", func(_ *astrophotography.Target, r *observatory.Reservation) {
			delete(r.Status.Session.Devices, "FilterWheel/east-wheel")
		}, astrophotography.ReasonNoFilter},
		{"an exposure that does not read", func(x *astrophotography.Target, _ *observatory.Reservation) {
			x.Spec.Sequences[0].Exposure = "five minutes"
		}, astrophotography.ReasonExposureFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				first, res := target("a-broken", lights(1)), readyOrNot(observatory.ReservationReady)
				c.change(first, res)
				w.put(targetsPath, first)
				w.put(targetsPath, target("b-next", astrophotography.Sequence{Exposure: "10s", Count: 1}))
				w.put(reservationsPath, res)
				w.playCamera(t, saves)
				w.start(t)
				waitUntil(t, func() bool { return w.imaging(t, "b-next") == "False SequencesDone" })
				if got := w.imaging(t, "a-broken"); got != "False "+c.reason {
					t.Errorf("Imaging %q, want False %s", got, c.reason)
				}
				status := w.targetNow(t, "a-broken").Status
				if status.Reservation != "" || status.Exposure != nil {
					t.Errorf("reservation %q, exposure %+v; want both cleared", status.Reservation, status.Exposure)
				}
			})
		})
	}
}

// claimsOf counts the claims of a target in the API server's history:
// each change of status.reservation from none to this night's
// reservation.
func (w *night) claimsOf(name string) int {
	w.api.mu.Lock()
	defer w.api.mu.Unlock()
	claims, held := 0, ""
	for _, e := range w.api.events {
		metadata, _ := e.object["metadata"].(map[string]any)
		if e.collection != targetsPath || metadata["namespace"] != ns || metadata["name"] != name {
			continue
		}
		status, _ := e.object["status"].(map[string]any)
		reservation, _ := status["reservation"].(string)
		if held == "" && reservation == tonight {
			claims++
		}
		held = reservation
	}
	return claims
}

// A target that failed on a reservation stays released after a restart
// of the operator, because the target's status records the failure.
// An edit of the spec makes it eligible again on the same reservation.
func TestAFailedTargetStaysReleased(t *testing.T) {
	cases := []struct {
		name   string
		after  func(*testing.T, *night)
		claims int
		want   string
		// failed is the reservation that status.failed names after.
		failed string
	}{
		{"after a restart", func(t *testing.T, w *night) {
			w.stop()
			w.start(t)
		}, 1, "False " + astrophotography.ReasonNoFilter, tonight},
		{"until an edit of the spec", func(t *testing.T, w *night) {
			fixed := w.targetNow(t, "m42")
			fixed.Spec.Sequences[0].Filter = "Luminance"
			fixed.Metadata.Generation = 2
			w.put(targetsPath, fixed)
		}, 2, "False " + astrophotography.ReasonSequencesDone, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				broken := target("m42", lights(1))
				broken.Spec.Sequences[0].Filter = "H_Beta"
				w.put(targetsPath, broken)
				w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
				w.playCamera(t, saves)
				w.start(t)
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False "+astrophotography.ReasonNoFilter })

				c.after(t, w)
				time.Sleep(time.Hour)
				synctest.Wait()

				if got := w.imaging(t, "m42"); got != c.want {
					t.Errorf("Imaging %q, want %q", got, c.want)
				}
				if got := w.claimsOf("m42"); got != c.claims {
					t.Errorf("%d claims, want %d", got, c.claims)
				}
				if got := failedReservation(w.targetNow(t, "m42")); got != c.failed {
					t.Errorf("status.failed names %q, want %q", got, c.failed)
				}
			})
		})
	}
}

func failedReservation(t *astrophotography.Target) string {
	if t.Status.Failed == nil {
		return ""
	}
	return t.Status.Failed.Reservation
}

// The end of the reservation clears its failures, so a new reservation
// of the same name claims the target again.
func TestTheEndOfAReservationClearsItsFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		broken := target("m42", lights(1))
		broken.Spec.Sequences[0].Filter = "H_Beta"
		w.put(targetsPath, broken)
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, func() bool { return failedReservation(w.targetNow(t, "m42")) == tonight })

		w.put(reservationsPath, readyOrNot(observatory.ReservationDeactivating))
		waitUntil(t, func() bool { return failedReservation(w.targetNow(t, "m42")) == "" })
	})
}

// A session with no imaging camera for the target fails it with
// NoTrain, before the runner reads a device.
func TestASessionWithNoCameraForTheTarget(t *testing.T) {
	res := readyOrNot(observatory.ReservationReady)
	res.Status.Session.Devices = nil
	_, err := (&runner{res: res}).prepare(target("m42", lights(1)))
	if failure, ok := err.(*imagingFailure); !ok || failure.reason != astrophotography.ReasonNoTrain {
		t.Errorf("%v, want NoTrain", err)
	}
}

// Another writer gives the target to another reservation during an
// exposure. The runner counts its frame, and then ends its work on the
// target, because the claim is no longer its own.
func TestATargetThatAnotherWriterTakesEndsTheWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		west := readyOrNot(observatory.ReservationReady)
		west.Metadata = meta("west-tonight")
		west.Status.Session.Devices = nil
		w.put(reservationsPath, west)
		w.put(targetsPath, target("m42", lights(3)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		taken := w.targetNow(t, "m42")
		taken.Status.Reservation = "west-tonight"
		w.put(targetsPath, taken)
		time.Sleep(time.Hour)
		synctest.Wait()
		status := w.targetNow(t, "m42").Status
		if status.Reservation != "west-tonight" || !slices.Equal(pendingOf(status), []int32{1}) {
			t.Errorf("reservation %q, pending %v; want west-tonight and [1]", status.Reservation, pendingOf(status))
		}
	})
}

// Another writer takes the target between the runner's read and its
// record of the next frame: the record meets a 409, finds the claim
// gone, and the runner exposes nothing more of the target.
func TestAClaimLostBeforeTheRecordExposesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		west := readyOrNot(observatory.ReservationReady)
		west.Metadata = meta("west-tonight")
		west.Status.Session.Devices = nil
		w.put(reservationsPath, west)
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		taken := false
		w.api.beforeStatus = func(_, key string, body map[string]any) bool {
			status, _ := body["status"].(map[string]any)
			if taken || key != ns+"/m42" || status["exposure"] == nil {
				return false
			}
			taken = true
			other := w.targetNow(t, "m42")
			other.Status.Reservation = "west-tonight"
			w.put(targetsPath, other)
			return false
		}
		w.start(t)
		time.Sleep(time.Hour)
		synctest.Wait()
		if !taken || len(w.worker.held()) != 0 {
			t.Errorf("taken %v, %d frames; want the record refused and no frame", taken, len(w.worker.held()))
		}
	})
}
