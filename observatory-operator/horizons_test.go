package main

// A Telescope that names a Horizon reports whether it exists, in its
// HorizonFound condition. A missing Horizon never stops a reservation,
// because only a scheduler reads a horizon.

import (
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// horizonOf answers a Telescope's HorizonFound condition as "<status>
// <reason>: <message>", or "" when the telescope has none.
func (w *world) horizonOf(telescope string) string {
	w.t.Helper()
	object, _ := decode[observatory.Telescope](w.t, w.api, kindCollection(observatory.TelescopeKind), telescope)
	c := conditionOf(object.Status.Conditions, observatory.ConditionHorizonFound)
	if c.Type == "" {
		return ""
	}
	return fmt.Sprintf("%s %s: %s", c.Status, c.Reason, c.Message)
}

// The example's east telescope names the Horizon yard, and west names
// none.
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
					t.Errorf("HorizonFound = %q, want %q", got, c.want)
				}
			})
		})
	}
}

// A Horizon deleted and created again moves the condition each time,
// and each transition posts one Event: a Warning while the horizon is
// missing, because a scheduler would plan with no horizon.
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
			t.Errorf("HorizonFound = %q, then %q", missing, w.horizonOf("east"))
		}
		// The recorder counts a repeat of an Event in its first copy.
		got := w.eventsWith(observatory.TelescopeKind, "east", "Found", reasonHorizonMissing)
		want := []string{
			"Normal Found: Found every parent x1",
			"Normal Found: Found Horizon yard x2",
			"Warning HorizonMissing: Missing Horizon yard x1",
		}
		if !slices.Equal(got, want) {
			t.Errorf("Events = %q, want %q", got, want)
		}
	})
}

// A telescope whose Horizon is missing still activates and runs.
func TestAMissingHorizonDoesNotStopAReservation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.deleteNamed(kindCollection(observatory.HorizonKind), "yard")

		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.settle()

		if got := w.horizonOf("east"); got != "False HorizonMissing: Missing Horizon yard" {
			t.Errorf("HorizonFound = %q", got)
		}
	})
}
