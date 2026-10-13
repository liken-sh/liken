package main

// A person's SetUp: the status writer keeps it on every kind, and
// expires it at dawn or at its setUpUntil.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The status writer composes and replaces every status while a
// reservation activates, and leaves each person's SetUp, and its
// setUpUntil, as the person wrote them.
func TestTheStatusWriterKeepsAPersonsSetUp(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		resources := []struct {
			kind observatory.Kind
			name string
		}{
			{observatory.ObservatoryKind, "lab"},
			{observatory.TelescopeKind, "east"},
			{observatory.MountKind, "east"},
			{observatory.CameraKind, "east-main"},
			{observatory.WeatherStationKind, "lab"},
			{observatory.OpticalTrainKind, "east-imaging"},
			{observatory.OpticalTubeKind, "east-refractor"},
			{observatory.GuiderKind, "east"},
		}
		until := time.Now().Add(6 * time.Hour)
		wrote := map[string]observatory.Condition{}
		for _, r := range resources {
			w.setUp(r.kind, r.name, true, until)
			wrote[r.kind.Name+"/"+r.name] = w.conditionOn(r.kind, r.name, observatory.ConditionSetUp)
		}
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		for _, r := range resources {
			if got, want := w.conditionOn(r.kind, r.name, observatory.ConditionSetUp), wrote[r.kind.Name+"/"+r.name]; got != want {
				t.Errorf("%s %s SetUp = %+v, want %+v", r.kind.Name, r.name, got, want)
			}
		}
		east, _ := decode[observatory.Telescope](t, w.api, kindCollection(observatory.TelescopeKind), "east")
		if east.Status.SetUpUntil == nil || !east.Status.SetUpUntil.Equal(until.UTC().Truncate(time.Second)) {
			t.Errorf("setUpUntil = %v, want %v", east.Status.SetUpUntil, until)
		}
	})
}

// A SetUp holds until the first astronomical dawn after the person
// wrote it, then turns False with the reason Expired, and its
// transition posts one Event, with no other change to wake the status
// writer.
func TestASetUpExpiresAtDawn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.operator()
		w.setUp(observatory.TelescopeKind, "east", true, time.Time{})
		dawn := labDawn(stamp())
		time.Sleep(time.Until(dawn) - time.Minute)
		synctest.Wait()
		if c := w.conditionOn(observatory.TelescopeKind, "east", observatory.ConditionSetUp); c.Status != observatory.ConditionTrue {
			t.Fatalf("SetUp before dawn = %+v", c)
		}

		time.Sleep(time.Minute + 2*statusWindow)
		synctest.Wait()
		message := "Expired at " + dawn.Format(time.RFC3339) + ": set up by stargazer"
		c := w.conditionOn(observatory.TelescopeKind, "east", observatory.ConditionSetUp)
		if c.Status != observatory.ConditionFalse || c.Reason != observatory.ReasonExpired || c.Message != message {
			t.Errorf("SetUp after dawn = %+v", c)
		}
		if got := w.setUpExpiryTime(observatory.TelescopeKind, "east"); !got.IsZero() {
			t.Errorf("setUpExpiryTime after dawn = %v, want none", got)
		}
		if got, want := w.eventsWith(observatory.TelescopeKind, "east", observatory.ReasonExpired), []string{"Normal Expired: " + message + " x1"}; !slices.Equal(got, want) {
			t.Errorf("Events = %v, want %v", got, want)
		}
	})
}

// A setUpUntil replaces dawn, on a device as on a Telescope.
func TestASetUpExpiresAtItsUntil(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.operator()
		until := stamp().Add(30 * time.Minute)
		w.setUp(observatory.MountKind, "east", true, until)
		time.Sleep(29 * time.Minute)
		synctest.Wait()
		if c := w.conditionOn(observatory.MountKind, "east", observatory.ConditionSetUp); c.Status != observatory.ConditionTrue {
			t.Fatalf("SetUp before its until = %+v", c)
		}
		time.Sleep(time.Minute + 2*statusWindow)
		synctest.Wait()
		c := w.conditionOn(observatory.MountKind, "east", observatory.ConditionSetUp)
		if c.Reason != observatory.ReasonExpired || c.Message != "Expired at "+until.Format(time.RFC3339)+": set up by stargazer" {
			t.Errorf("SetUp after its until = %+v", c)
		}
	})
}

// The status writer shows when each SetUp expires in
// status.setUpExpiryTime: at the person's setUpUntil when they wrote
// one, else at the next astronomical dawn, and nothing while the
// resource is not set up. The same holds on an Observatory, a
// Telescope, and a device.
func TestTheStatusShowsWhenASetUpExpires(t *testing.T) {
	cases := []struct {
		name  string
		kind  observatory.Kind
		up    bool
		until func(time.Time) time.Time
		want  func(time.Time) time.Time
	}{
		{"a telescope until dawn", observatory.TelescopeKind, true, noTime, labDawn},
		{"a device until dawn", observatory.MountKind, true, noTime, labDawn},
		{"an observatory until dawn", observatory.ObservatoryKind, true, noTime, labDawn},
		{"a telescope until its setUpUntil", observatory.TelescopeKind, true, inTwoHours, inTwoHours},
		{"a device until its setUpUntil", observatory.MountKind, true, inTwoHours, inTwoHours},
		{"a telescope taken down", observatory.TelescopeKind, false, noTime, noTime},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.operator()
				name := map[string]string{observatory.ObservatoryKind.Name: "lab"}[c.kind.Name]
				name = firstNonEmpty(name, "east")
				now := stamp()
				w.setUp(c.kind, name, c.up, c.until(now))
				time.Sleep(2 * statusWindow)
				synctest.Wait()
				if got, want := w.setUpExpiryTime(c.kind, name), c.want(now); !got.Equal(want) {
					t.Errorf("setUpExpiryTime = %v, want %v", got, want)
				}
			})
		})
	}
}

func noTime(time.Time) time.Time { return time.Time{} }

func inTwoHours(now time.Time) time.Time { return now.Add(2 * time.Hour) }

// A SetUp that expired lets no new session start: the next reservation
// waits for a person again.
func TestAnExpiredSetUpHoldsTheNextReservation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.require(observatory.TelescopeKind, "east", setUpRequired)
		w.setUp(observatory.TelescopeKind, "east", true, stamp().Add(time.Hour))
		time.Sleep(time.Hour + 2*statusWindow)
		synctest.Wait()
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.stepWaits("east-tonight", observatory.StepRequires, "Waiting for Telescope east SetUp=True")
	})
}
