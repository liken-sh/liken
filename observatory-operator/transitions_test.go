package main

// The status writer posts one Event for each condition transition of a
// resource, with the condition's reason and message.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// typedEvents returns "type reason: message" for each Event about one
// object, oldest first.
func typedEvents(a *fakeAPI, kind observatory.Kind, name string) []string {
	var out []string
	for _, e := range a.recorded.About(kind.Name, name) {
		out = append(out, e.Type+" "+e.Reason+": "+e.Message)
	}
	return out
}

// The camera's Cooling condition appears after the camera connects, so
// stateconditions_test.go tests its Event.
func TestADevicePostsItsReadyTransitions(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)

		got := slices.DeleteFunc(typedEvents(w.api, observatory.CameraKind, "east-main"), func(e string) bool {
			return strings.HasPrefix(e, "Normal CoolerOff: ") || strings.HasPrefix(e, "Normal Procedure")
		})

		want := []string{
			"Normal Found: Found every parent",
			"Normal Connected: Connected on east-telescope",
		}
		for _, event := range want {
			if !slices.Contains(got, event) {
				t.Errorf("the Events are %q, want %q among them", got, event)
			}
		}
		if got[len(got)-1] != want[1] || len(slices.Compact(slices.Clone(got))) != len(got) {
			t.Errorf("Events = %q, want each transition once, ending on Connected", got)
		}
	})
}

func TestAFailureAndAMissingParentPostWarnings(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.refuse("CCD Simulator", "CCD_GAIN")
		w.api.deleteNamed(podsCollection, "east-main-camera")
		fault := "Warning Error: Failed: "
		missing := "Warning ParentMissing: Missing Telescope east"

		w.until(time.Minute, "no Warning Event has the reason Error", func() bool {
			return slices.ContainsFunc(typedEvents(w.api, observatory.CameraKind, "east-main"), func(e string) bool { return strings.HasPrefix(e, fault) })
		})
		w.api.deleteNamed(kindCollection(observatory.TelescopeKind), "east")
		w.until(time.Minute, "no Warning Event has the reason ParentMissing", func() bool {
			return slices.Contains(typedEvents(w.api, observatory.MountKind, "east"), missing)
		})
	})
}
