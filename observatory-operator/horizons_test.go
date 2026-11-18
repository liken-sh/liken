package main

// A missing Horizon does not stop a reservation, because only a
// scheduler reads a Horizon.

import (
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// horizonOf returns a Telescope's HorizonFound condition as "<status>
// <reason>: <message>", or "" when the Telescope has none.
func (w *world) horizonOf(telescope string) string {
	w.t.Helper()
	object, _ := decode[observatory.Telescope](w.t, w.api, kindCollection(observatory.TelescopeKind), telescope)
	c := conditionOf(object.Status.Conditions, observatory.ConditionHorizonFound)
	if c.Type == "" {
		return ""
	}
	return fmt.Sprintf("%s %s: %s", c.Status, c.Reason, c.Message)
}

// In the example, the east Telescope's spec.horizon is yard, and the
// west Telescope has none.
func TestATelescopeReportsTheHorizonItNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, telescope, horizon, want string
	}{
		{"a horizon that exists", "east", "yard", "True Found: Found Horizon yard"},
		{"another horizon that exists", "east", "roof", "True Found: Found Horizon roof"},
		{"a horizon that does not exist", "east", "garden", "False HorizonMissing: Missing Horizon garden"},
		{"no horizon", "east", "", ""},
		{"a telescope the example gives no horizon", "west", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.amend(observatory.TelescopeKind, c.telescope, func(spec map[string]any) {
					spec["horizon"] = c.horizon
					if c.horizon == "" {
						delete(spec, "horizon")
					}
				})
				w.settle()

				if got := w.horizonOf(c.telescope); got != c.want {
					t.Errorf("the HorizonFound condition is %q, want %q", got, c.want)
				}
			})
		})
	}
}

// Deleting a Horizon and creating it again changes the condition each
// time, and each transition posts one Event. The Event is a Warning
// while the Horizon is missing, because a scheduler would plan with no
// horizon.
func TestAHorizonThatComesAndGoesPostsEachTransition(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.settle()
		yard, _ := w.api.object(kindCollection(observatory.HorizonKind), "yard")

		w.api.deleteNamed(kindCollection(observatory.HorizonKind), "yard")
		w.settle()
		missing := w.horizonOf("east")
		w.api.put(kindCollection(observatory.HorizonKind), yard)
		w.settle()

		if missing != "False HorizonMissing: Missing Horizon yard" || w.horizonOf("east") != "True Found: Found Horizon yard" {
			t.Errorf("the HorizonFound condition is %q after the delete and %q after the create, want %q and %q", missing, w.horizonOf("east"), "False HorizonMissing: Missing Horizon yard", "True Found: Found Horizon yard")
		}
		// The recorder counts a repeated Event on the first one, as x2.
		got := w.eventsWith(observatory.TelescopeKind, "east", "Found", reasonHorizonMissing)
		want := []string{
			"Normal Found: Found every parent x1",
			"Normal Found: Found Horizon yard x2",
			"Warning HorizonMissing: Missing Horizon yard x1",
		}
		if !slices.Equal(got, want) {
			t.Errorf("the Telescope Events are %q, want %q", got, want)
		}
	})
}

// A Telescope whose Horizon is missing still activates and runs.
func TestAMissingHorizonDoesNotStopAReservation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.deleteNamed(kindCollection(observatory.HorizonKind), "yard")

		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.settle()

		if got := w.horizonOf("east"); got != "False HorizonMissing: Missing Horizon yard" {
			t.Errorf("the HorizonFound condition is %q, want %q", got, "False HorizonMissing: Missing Horizon yard")
		}
	})
}
