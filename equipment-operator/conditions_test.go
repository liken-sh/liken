package main

// Tests that each condition transition posts an Event, against the fake
// Events API and the fake clock of a synctest bubble.

import (
	"io"
	"net/http"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

// testRecorder returns a Recorder that posts Events to the test's fake
// API server until the test ends.
func testRecorder(t *testing.T, client *Client) *events.Recorder {
	t.Helper()
	return events.New(t.Context(), client.Client, "equipment-operator", events.Options{Instance: "equipment-operator-0", Log: io.Discard})
}

// posted is the part of an Event that the tests compare: its type, its
// reason, and its count.
type posted struct {
	kind, reason string
	count        int32
}

// postedAbout returns the Events the fake has for one object. It first
// waits until every goroutine in the synctest bubble, including the
// recorder's, is idle, so all pending Events have been posted.
func postedAbout(recorded *eventstest.Events, kind, name string) []posted {
	synctest.Wait()
	var out []posted
	for _, event := range recorded.About(kind, name) {
		out = append(out, posted{event.Type, event.Reason, event.Count})
	}
	return out
}

func conditionWith(kind string, status ConditionStatus, reason string) Condition {
	return Condition{Type: kind, Status: status, Reason: reason, Message: "a message"}
}

func TestEachConditionTransitionPostsOneEvent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		previous []Condition
		written  Condition
		want     []posted
	}{
		{"a new condition", nil, conditionWith(reachableConditionType, ConditionUnknown, reasonConnecting),
			[]posted{{events.TypeNormal, reasonConnecting, 1}}},
		{"a receiver that stops answering", []Condition{conditionWith(reachableConditionType, ConditionTrue, reasonConnected)},
			conditionWith(reachableConditionType, ConditionFalse, reasonUnreachable), []posted{{events.TypeWarning, reasonUnreachable, 1}}},
		{"a new message alone", []Condition{conditionWith(reachableConditionType, ConditionTrue, reasonConnected)},
			Condition{Type: reachableConditionType, Status: ConditionTrue, Reason: reasonConnected, Message: "another"}, nil},
		{"an input the receiver cannot report", []Condition{conditionWith(inputSelectedConditionType, ConditionTrue, reasonSessionInput)},
			conditionWith(inputSelectedConditionType, ConditionUnknown, reasonUnreachable), []posted{{events.TypeNormal, reasonUnreachable, 1}}},
		{"a bus in Listen", nil, conditionWith(conditionScanned, ConditionFalse, reasonListening),
			[]posted{{events.TypeNormal, reasonListening, 1}}},
		{"a bus whose polls nobody answers", []Condition{conditionWith(conditionScanned, ConditionUnknown, reasonScanning)},
			conditionWith(conditionScanned, ConditionFalse, reasonNoAnswer), []posted{{events.TypeWarning, reasonNoAnswer, 1}}},
		{"an adapter gone stale", []Condition{conditionWith(conditionJoined, ConditionTrue, "Joined")},
			conditionWith(conditionJoined, ConditionUnknown, reasonStale), []posted{{events.TypeWarning, reasonStale, 1}}},
		{"a power the TV did not confirm", nil, conditionWith(conditionPowerApplied, ConditionFalse, reasonUnconfirmed),
			[]posted{{events.TypeWarning, reasonUnconfirmed, 1}}},
		{"a wake a new power ended", []Condition{conditionWith(conditionWakeApplied, ConditionUnknown, reasonWaking)},
			conditionWith(conditionWakeApplied, ConditionFalse, reasonSuperseded), []posted{{events.TypeNormal, reasonSuperseded, 1}}},
		{"another Television in charge", nil, conditionWith(conditionInCharge, ConditionFalse, reasonAnotherInCharge),
			[]posted{{events.TypeNormal, reasonAnotherInCharge, 1}}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				recorded := &eventstest.Events{}
				client := testAPIClient(t, recorded.Around(http.NotFoundHandler()))
				about := reference("Television", ObjectMeta{Name: "lounge", UID: "uid-1"})

				postTransitions(testRecorder(t, client), about, one.previous, []Condition{one.written})

				mustDeepEqual(t, postedAbout(recorded, "Television", "lounge"), one.want)
			})
		})
	}
}

// An Event about a cluster-scoped object is posted in the `default`
// namespace.
func TestAnEventAboutATelevisionGoesInDefault(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		recorded := &eventstest.Events{}
		client := testAPIClient(t, recorded.Around(http.NotFoundHandler()))

		postTransitions(testRecorder(t, client), reference("Television", ObjectMeta{Name: "lounge"}), nil,
			[]Condition{conditionWith(conditionReachable, ConditionTrue, reasonAnswers)})
		synctest.Wait()

		list := recorded.List()
		mustMatch(t, len(list), 1)
		mustMatch(t, list[0].Metadata.Namespace, "default")
		mustMatch(t, list[0].InvolvedObject.APIVersion, equipmentAPIVersion)
		mustMatch(t, list[0].Message, "a message")
	})
}

func TestSentencesStartsEachFragmentWithACapitalLetter(t *testing.T) {
	cases := []struct {
		name, message, want string
	}{
		{"one fragment", "the TV already reported On", "The TV already reported On"},
		{"two fragments", "the TV already reported On; the adapter on node-1 sent Active Source", "The TV already reported On. The adapter on node-1 sent Active Source"},
		{"a field path first", "spec.power at generation 2 is On; the wake stopped", "spec.power at generation 2 is On. The wake stopped"},
		{"a sentence already", "Every adapter scanned the bus", "Every adapter scanned the bus"},
		{"no message", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mustMatch(t, sentences(c.message), c.want)
		})
	}
}
