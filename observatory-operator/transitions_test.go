package main

// Each condition transition of a resource that the status writer
// writes posts one Event, with the condition's reason and message.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// typedEvents answers the type, the reason, and the message of each
// Event about one object, in the order the operator wrote them.
func typedEvents(a *fakeAPI, kind observatory.Kind, name string) []string {
	var out []string
	for _, e := range a.recorded.About(kind.Name, name) {
		out = append(out, e.Type+" "+e.Reason+": "+e.Message)
	}
	return out
}

// A device posts an Event for each condition it first reports, and
// each time its Ready condition changes its reason. The activation
// that connects it ends on one Connected Event. The camera's Cooling
// condition, which appears once it is connected, posts the last one,
// and stateconditions_test.go holds that.
func TestADevicePostsItsReadyTransitions(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)

		got := slices.DeleteFunc(typedEvents(w.api, observatory.CameraKind, "east-main"), func(e string) bool {
			return strings.HasPrefix(e, "Normal CoolerOff: ")
		})

		want := []string{
			"Normal Found: Found every parent",
			"Normal Connected: Connected on east-telescope",
		}
		for _, event := range want {
			if !slices.Contains(got, event) {
				t.Errorf("no Event %q in %q", event, got)
			}
		}
		if got[len(got)-1] != want[1] || len(slices.Compact(slices.Clone(got))) != len(got) {
			t.Errorf("Events = %q, want each transition once, ending on Connected", got)
		}
	})
}

// A device that fails posts a Warning with the failure, and a resource
// whose parent is missing posts a Warning that names it.
func TestAFailureAndAMissingParentPostWarnings(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.refuse("CCD Simulator", "CCD_GAIN")
		w.api.deleteNamed(podsCollection, "east-main-camera")
		fault := "Warning Error: Failed: "
		missing := "Warning ParentMissing: Missing Telescope east"

		w.until(time.Minute, "no Warning is written", func() bool {
			return slices.ContainsFunc(typedEvents(w.api, observatory.CameraKind, "east-main"), func(e string) bool { return strings.HasPrefix(e, fault) })
		})
		w.api.deleteNamed(kindCollection(observatory.TelescopeKind), "east")
		w.until(time.Minute, "no Warning is written", func() bool {
			return slices.Contains(typedEvents(w.api, observatory.MountKind, "east"), missing)
		})
	})
}
