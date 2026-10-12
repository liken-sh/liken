package main

// A Reservation's SessionCurrent condition says whether a spec edit
// waits for the next session: the session record holds the outbox and
// the merged FITS keywords, and an edit of either takes effect only
// when a new session records them.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// sessionCurrent answers the SessionCurrent condition of east-tonight
// as "<status> <reason>: <message>", or "" when it has none.
func (w *world) sessionCurrent() string {
	w.t.Helper()
	r, _ := w.reservation("east-tonight")
	c := conditionOf(r.Status.Conditions, observatory.ConditionSessionCurrent)
	if c.Type == "" {
		return ""
	}
	return string(c.Status) + " " + c.Reason + ": " + c.Message
}

const noEditPending = "True NoEditPending: No edit waits for the next session"

func TestAReservationShowsTheEditsThatWaitForTheNextSession(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(w *world)
		want string
	}{
		{"no edit", func(w *world) {}, noEditPending},
		{"an edit of the holder, which takes effect at once", func(w *world) {
			w.amend(observatory.ReservationKind, "east-tonight", func(spec map[string]any) { spec["holder"] = "laptop" })
		}, noEditPending},
		{"another outbox", func(w *world) {
			w.amend(observatory.ReservationKind, "east-tonight", func(spec map[string]any) {
				spec["outbox"] = map[string]any{"claimName": "nightly"}
			})
		}, "False EditPending: spec.outbox.claimName changes from frames to nightly at the next session"},
		{"no outbox", func(w *world) {
			w.amend(observatory.ReservationKind, "east-tonight", func(spec map[string]any) { delete(spec, "outbox") })
		}, "False EditPending: spec.outbox.claimName changes from frames to none at the next session"},
		{"a keyword of the reservation", func(w *world) {
			w.declare(observatory.ReservationKind, "east-tonight", fitsKeyword("OBJECT", "M33", ""))
		}, `False EditPending: FITS keyword OBJECT of Camera east-main changes from none to "M33" at the next session`},
		{"a keyword of the Observatory", func(w *world) {
			w.declare(observatory.ObservatoryKind, "lab", fitsKeyword("OBSERVER", "Edited Observer", ""))
		}, `False EditPending: FITS keyword OBSERVER of Camera east-main changes from "Simulated Observer" to "Edited Observer" at the next session`},
		{"a keyword of the Observatory that the Telescope's replaces", func(w *world) {
			w.declare(observatory.ObservatoryKind, "lab", fitsKeyword("OBSERVER", "Simulated Observer", ""), fitsKeyword("TELESCOP", "Lab scope", ""))
		}, noEditPending},
		{"the comment of a keyword of the Telescope", func(w *world) {
			w.declare(observatory.TelescopeKind, "east", fitsKeyword("TELESCOP", "East refractor", "the refractor"))
		}, `False EditPending: the comment of FITS keyword TELESCOP of Camera east-main changes from "the Telescope's own name" to "the refractor" at the next session`},
		{"a keyword of the Camera and the outbox", func(w *world) {
			w.declare(observatory.CameraKind, "east-main", fitsKeyword("INSTRUME", "East main camera", ""))
			w.amend(observatory.ReservationKind, "east-tonight", func(spec map[string]any) {
				spec["outbox"] = map[string]any{"claimName": "nightly"}
			})
		}, `False EditPending: spec.outbox.claimName changes from frames to nightly at the next session; ` +
			`FITS keyword INSTRUME of Camera east-main changes from none to "East main camera" at the next session`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := outboxWorld(t, "frames")
				c.edit(w)
				w.settle()

				if got := w.sessionCurrent(); got != c.want {
					t.Errorf("SessionCurrent = %q, want %q", got, c.want)
				}
			})
		})
	}
}

// The condition posts one Event for each transition: True when PowerOn
// records the session, False at the first edit that waits, nothing for
// a second edit that changes only the message, and True when the spec
// matches the record again. An operator that restarts posts none.
func TestEachTransitionOfSessionCurrentPostsOneEvent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := outboxWorld(t, "frames")
		outbox := func(claim string) func(spec map[string]any) {
			return func(spec map[string]any) { spec["outbox"] = map[string]any{"claimName": claim} }
		}
		w.amend(observatory.ReservationKind, "east-tonight", outbox("nightly"))
		w.settle()
		w.amend(observatory.ReservationKind, "east-tonight", outbox("weekly"))
		w.settle()
		w.restart()
		w.settle()
		w.amend(observatory.ReservationKind, "east-tonight", outbox("frames"))
		w.settle()

		var got []string
		for _, e := range w.api.recorded.About(observatory.ReservationKind.Name, "east-tonight") {
			if e.Reason == reasonEditPending || e.Reason == reasonNoEditPending {
				got = append(got, e.Type+" "+e.Reason+": "+e.Message)
			}
		}
		want := []string{
			"Normal NoEditPending: No edit waits for the next session",
			"Normal EditPending: spec.outbox.claimName changes from frames to nightly at the next session",
			"Normal NoEditPending: No edit waits for the next session",
		}
		if !slices.Equal(got, want) {
			t.Errorf("Events = %q, want %q", got, want)
		}
	})
}

// A reservation has the condition only while it has a session: not
// while it waits for spec.start, and not once it is Released.
func TestOnlyAReservationWithASessionHasSessionCurrent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		spec  map[string]any
		phase observatory.ReservationPhase
	}{
		{"Scheduled for a later start", map[string]any{"start": "2100-01-01T00:00:00Z"}, observatory.ReservationScheduled},
		{"Released at its end", map[string]any{"end": "2000-01-01T00:00:00Z"}, observatory.ReservationReleased},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				c.spec["telescope"], c.spec["holder"] = "east", "desktop"
				w.reserve("east-tonight", c.spec)
				w.phase("east-tonight", c.phase, 15*time.Minute)
				w.settle()

				if got := w.sessionCurrent(); got != "" {
					t.Errorf("SessionCurrent = %q, want none", got)
				}
			})
		})
	}
}

// A session that ends removes the condition.
func TestAReleasedReservationHasNoSessionCurrent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		end := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": end})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.settle()
		if got := w.sessionCurrent(); got != noEditPending {
			t.Fatalf("SessionCurrent while Ready = %q, want %q", got, noEditPending)
		}
		time.Sleep(time.Hour)
		w.phase("east-tonight", observatory.ReservationReleased, 10*time.Minute)
		w.settle()

		if got := w.sessionCurrent(); got != "" {
			t.Errorf("SessionCurrent = %q, want none", got)
		}
	})
}
