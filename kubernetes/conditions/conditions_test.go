package conditions_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// earlier is the LastTransitionTime of the existing condition in each case.
var earlier = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func ready(status conditions.Status, reason, message string) conditions.Condition {
	return conditions.Condition{Type: "Ready", Status: status, Reason: reason, Message: message}
}

// Set changes lastTransitionTime only when the status changes. It
// returns true for a new condition, a new status, or a new reason. A
// new message alone is not a transition, but Set stores it.
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
					t.Errorf("Set returned %t and the list has %+v among %d conditions, want %t and %+v in 1", transitioned, got, len(list), c.transitioned, want)
				}
			})
		})
	}
}

// A transition uses the caller's LastTransitionTime when it is set, and
// otherwise the current time truncated to the second, in UTC.
func TestSetUsesTheCallersTransitionTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		time.Sleep(1500 * time.Millisecond)
		var list []conditions.Condition
		provided := ready(conditions.True, "Connected", "Connected")
		provided.LastTransitionTime = earlier

		conditions.Set(&list, ready(conditions.False, "Starting", "Starting"))
		unset, _ := conditions.Find(list, "Ready")
		conditions.Set(&list, provided)
		got, _ := conditions.Find(list, "Ready")

		if want := time.Now().UTC().Truncate(time.Second); unset.LastTransitionTime != want || unset.LastTransitionTime.Location() != time.UTC {
			t.Errorf("an unset time became %v, want %v in UTC", unset.LastTransitionTime, want)
		}
		if got.LastTransitionTime != earlier {
			t.Errorf("a provided time became %v, want %v", got.LastTransitionTime, earlier)
		}
	})
}

func TestSetKeepsTheOtherConditionsInOrder(t *testing.T) {
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
		t.Errorf("the list is %v, want %v", got, want)
	}
}

func TestFindReturnsFalseForAMissingType(t *testing.T) {
	if c, ok := conditions.Find([]conditions.Condition{{Type: "Ready"}}, "Safe"); ok || c != (conditions.Condition{}) {
		t.Errorf("Find returned %+v, %t; want the zero condition and false", c, ok)
	}
}
