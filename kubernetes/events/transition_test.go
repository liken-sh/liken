package events_test

import (
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
)

// Each condition transition posts one Event with the condition's
// reason and message, a Warning when the new status is the bad one.
// A new message alone posts nothing.
func TestAConditionTransitionPostsOneEvent(t *testing.T) {
	connected := conditions.Condition{Type: "Ready", Status: conditions.True, Reason: "Connected", Message: "Connected on east"}
	cases := []struct {
		name string
		next conditions.Condition
		bad  conditions.Status
		want []string
	}{
		{"to the bad status", conditions.Condition{Type: "Ready", Status: conditions.False, Reason: "Error", Message: "The driver refused CONNECT."}, conditions.False, []string{"Warning Error: The driver refused CONNECT."}},
		{"to another status", conditions.Condition{Type: "Ready", Status: conditions.False, Reason: "Disconnecting", Message: "Disconnecting"}, conditions.True, []string{"Normal Disconnecting: Disconnecting"}},
		{"with no bad status", conditions.Condition{Type: "Ready", Status: conditions.False, Reason: "Disconnecting", Message: "Disconnecting"}, "", []string{"Normal Disconnecting: Disconnecting"}},
		{"to a new reason", conditions.Condition{Type: "Ready", Status: conditions.True, Reason: "Reconnected", Message: "Connected on east"}, conditions.False, []string{"Normal Reconnected: Connected on east"}},
		{"to a new message", conditions.Condition{Type: "Ready", Status: conditions.True, Reason: "Connected", Message: "Connected on west"}, conditions.False, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := record(t, notFound)
				list := []conditions.Condition{connected}

				transitioned := r.recorder.SetCondition(mount, &list, c.next, c.bad)
				synctest.Wait()

				var got []string
				for _, event := range r.held.About("Mount", "east-mount") {
					got = append(got, event.Type+" "+event.Reason+": "+event.Message)
				}
				held, _ := conditions.Find(list, "Ready")
				if transitioned != (c.want != nil) || len(got) != len(c.want) || (len(got) == 1 && got[0] != c.want[0]) || held.Message != c.next.Message {
					t.Errorf("SetCondition answered %t, posted %q, and holds %q; want %t, %q, and %q", transitioned, got, held.Message, c.want != nil, c.want, c.next.Message)
				}
			})
		})
	}
}

// A new condition is a transition too: the object has its first
// verdict.
func TestANewConditionPostsAnEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := record(t, notFound)
		var list []conditions.Condition

		r.recorder.SetCondition(mount, &list, conditions.Condition{Type: "ParentFound", Status: conditions.False, Reason: "ParentMissing", Message: "Missing Telescope east"}, conditions.False)
		synctest.Wait()

		if got := r.held.About("Mount", "east-mount"); len(got) != 1 || got[0].Type != events.TypeWarning || got[0].Reason != "ParentMissing" {
			t.Errorf("the server holds %+v, want one ParentMissing Warning", got)
		}
	})
}
