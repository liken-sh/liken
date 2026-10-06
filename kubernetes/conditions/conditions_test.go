package conditions_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// earlier is the transition time of the stored condition in each case.
var earlier = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func ready(status conditions.Status, reason, message string) conditions.Condition {
	return conditions.Condition{Type: "Ready", Status: status, Reason: reason, Message: message}
}

// Set moves lastTransitionTime only when the status changes, and
// reports a transition for a new condition, a new status, or a new
// reason. A new message alone is no transition, but the list holds it.
func TestSetKeepsTheTransitionTimeUntilTheStatusChanges(t *testing.T) {
	stored := conditions.Condition{Type: "Ready", Status: conditions.True, Reason: "Connected", Message: "Connected", LastTransitionTime: earlier}
	cases := []struct {
		name         string
		list         []conditions.Condition
		next         conditions.Condition
		transitioned bool
		movesTime    bool
	}{
		{"a new condition", nil, ready(conditions.False, "Starting", "Starting"), true, true},
		{"a new status", []conditions.Condition{stored}, ready(conditions.False, "Error", "Failed"), true, true},
		{"a new reason", []conditions.Condition{stored}, ready(conditions.True, "Reconnected", "Connected"), true, false},
		{"a new message", []conditions.Condition{stored}, ready(conditions.True, "Connected", "Connected on east"), false, false},
		{"the same condition", []conditions.Condition{stored}, ready(conditions.True, "Connected", "Connected"), false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				list := c.list

				transitioned := conditions.Set(&list, c.next)

				got, _ := conditions.Find(list, "Ready")
				want := c.next
				want.LastTransitionTime = earlier
				if c.movesTime {
					want.LastTransitionTime = time.Now().UTC()
				}
				if transitioned != c.transitioned || got != want || len(list) != 1 {
					t.Errorf("Set answered %t and holds %+v in %d conditions, want %t and %+v in 1", transitioned, got, len(list), c.transitioned, want)
				}
			})
		})
	}
}

// A transition takes the caller's time when it states one, and the
// present time to the second otherwise, because the API server keeps
// a date-time to the second.
func TestSetTakesTheCallersTransitionTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		time.Sleep(1500 * time.Millisecond)
		var list []conditions.Condition
		stated := ready(conditions.True, "Connected", "Connected")
		stated.LastTransitionTime = earlier

		conditions.Set(&list, ready(conditions.False, "Starting", "Starting"))
		unstated, _ := conditions.Find(list, "Ready")
		conditions.Set(&list, stated)
		got, _ := conditions.Find(list, "Ready")

		if want := time.Now().UTC().Truncate(time.Second); unstated.LastTransitionTime != want || unstated.LastTransitionTime.Location() != time.UTC {
			t.Errorf("an unstated time became %v, want %v in UTC", unstated.LastTransitionTime, want)
		}
		if got.LastTransitionTime != earlier {
			t.Errorf("a stated time became %v, want %v", got.LastTransitionTime, earlier)
		}
	})
}

// Set changes only the condition of its type, and keeps the order of
// the list.
func TestSetLeavesTheOtherConditionsInPlace(t *testing.T) {
	list := []conditions.Condition{
		{Type: "ParentFound", Status: conditions.True, Reason: "Found"},
		{Type: "Ready", Status: conditions.True, Reason: "Connected"},
	}

	conditions.Set(&list, ready(conditions.False, "Error", "Failed"))
	conditions.Set(&list, conditions.Condition{Type: "Safe", Status: conditions.Unknown, Reason: "Unknown"})

	types := []string{}
	for _, c := range list {
		types = append(types, c.Type+"="+string(c.Status))
	}
	if got, want := types, []string{"ParentFound=True", "Ready=False", "Safe=Unknown"}; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("the list holds %v, want %v", got, want)
	}
}

// Find answers false for a type the list does not hold.
func TestFindAnswersFalseForAnAbsentType(t *testing.T) {
	if c, ok := conditions.Find([]conditions.Condition{{Type: "Ready"}}, "Safe"); ok || c != (conditions.Condition{}) {
		t.Errorf("Find answered %+v, %t; want the zero condition and false", c, ok)
	}
}
