package main

// Which target a reservation claims, and two runners that claim the
// same target at once.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func TestTargetsAreClaimedByPriorityThenAgeThenName(t *testing.T) {
	at := func(name string, priority int32, day int) astrophotography.Target {
		created := time.Date(2026, 10, day, 0, 0, 0, 0, time.UTC)
		x := target(name, lights(1))
		x.Spec.Priority, x.Metadata.CreationTimestamp = priority, &created
		return *x
	}
	targets := []astrophotography.Target{
		at("old-low", 0, 1), at("new-high", 5, 9), at("b-mid", 2, 3), at("a-mid", 2, 3), at("old-mid", 2, 2),
	}
	slices.SortFunc(targets, byPriority)
	var names []string
	for _, x := range targets {
		names = append(names, x.Metadata.Name)
	}
	want := []string{"new-high", "old-mid", "a-mid", "b-mid", "old-low"}
	if !slices.Equal(names, want) {
		t.Errorf("byPriority sorts the targets as %q, want %q", names, want)
	}
}

func TestEligibleTargets(t *testing.T) {
	session := reservationIn(observatory.ReservationReady).Status.Session
	twoCameras := reservationIn(observatory.ReservationReady).Status.Session
	twoCameras.Devices["Camera/west-main"] = observatory.SessionDevice{OpticalTrain: "west-imaging"}
	ready := map[string]bool{tonight: true, "west-tonight": true}
	cases := []struct {
		name     string
		change   func(*astrophotography.Target)
		session  *observatory.Session
		eligible bool
	}{
		{"a target with work left", func(*astrophotography.Target) {}, session, true},
		{"a suspended target", func(x *astrophotography.Target) { x.Spec.Suspend = true }, session, false},
		{"a complete target", func(x *astrophotography.Target) {
			x.Status.Conditions = []conditions.Condition{{Type: astrophotography.ConditionComplete, Status: conditions.True}}
		}, session, false},
		{"a target whose frames all wait in the outbox", func(x *astrophotography.Target) {
			x.Status.Sequences = []astrophotography.SequenceProgress{{Sequence: lights(1), Pending: 1}}
		}, session, false},
		{"a target another Ready reservation images", func(x *astrophotography.Target) { x.Status.Reservation = "west-tonight" }, session, false},
		{"a target this reservation images", func(x *astrophotography.Target) { x.Status.Reservation = tonight }, session, true},
		{"a target a reservation that ended imaged", func(x *astrophotography.Target) { x.Status.Reservation = "last-night" }, session, true},
		{"a target on another train", func(x *astrophotography.Target) { x.Spec.OpticalTrain = "west-imaging" }, session, false},
		{"a target with no train, and one imaging camera", func(x *astrophotography.Target) { x.Spec.OpticalTrain = "" }, session, true},
		{"a target with no train, and two imaging cameras", func(x *astrophotography.Target) { x.Spec.OpticalTrain = "" }, twoCameras, false},
		{"a target being deleted", func(x *astrophotography.Target) { x.Metadata.DeletionTimestamp = &time.Time{} }, session, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			x := target("m42", lights(1))
			c.change(x)
			if got := eligible(x, c.session, ready, tonight); got != c.eligible {
				t.Errorf("eligible = %v, want %v for %s", got, c.eligible, c.name)
			}
		})
	}
}

// A guide camera images nothing, so a target with no train is eligible
// on a telescope with one imaging camera and a guide camera.
func TestAGuideCameraIsNoImagingCamera(t *testing.T) {
	session := reservationIn(observatory.ReservationReady).Status.Session
	session.Devices["Camera/east-guide"] = observatory.SessionDevice{OpticalTrain: "east-guide", Guides: true}
	x := target("m42", lights(1))
	x.Spec.OpticalTrain = ""
	if got, ok := cameraFor(session, x); !ok || got.name != "east-main" {
		t.Errorf("cameraFor returned %+v, %v, want east-main", got, ok)
	}
}

// Two runners pick the same target at once. The other runner's claim
// is written first, so this runner's write gets a 409. It reads the
// target again, finds it claimed, claims the next target, and posts no
// Event on the target it lost.
func TestAClaimThatLosesARaceClaimsTheNextTarget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		first, second := target("a-first", lights(1)), target("b-second", lights(1))
		w.put(targetsPath, first)
		w.put(targetsPath, second)
		west := reservationIn(observatory.ReservationReady)
		west.Metadata = meta("west-tonight")
		west.Status.Session.Devices = nil
		w.put(reservationsPath, west)
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		raced := false
		w.api.beforeStatus = func(collection, key string, _ map[string]any) bool {
			if collection != targetsPath || key != ns+"/a-first" || raced {
				return false
			}
			raced = true
			claimed := w.targetNow(t, "a-first")
			claimed.Status.Reservation = "west-tonight"
			w.put(targetsPath, claimed)
			return false
		}
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "b-second") == "False SequencesDone" })
		if !raced {
			t.Fatal("the runner never wrote a-first")
		}
		if got := w.targetNow(t, "a-first").Status.Reservation; got != "west-tonight" {
			t.Errorf("a-first is claimed by %q, want west-tonight", got)
		}
		if got := w.eventReasons("Target", "a-first"); len(got) != 0 {
			t.Errorf("the target the runner lost has Events %q, want none", got)
		}
	})
}
