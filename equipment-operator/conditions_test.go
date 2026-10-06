package main

// The Event that each condition transition posts, against the fake of
// the events collection, on the fake clock of a synctest bubble.

import (
	"io"
	"net/http"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

// testRecorder posts Events through a test's fake API server until the
// test ends.
func testRecorder(t *testing.T, client *Client) *events.Recorder {
	t.Helper()
	return events.New(t.Context(), client.Client, "equipment-operator", events.Options{Instance: "equipment-operator-0", Log: io.Discard})
}

// posted is the part of an Event a test asserts: its type, its reason,
// and how many times it was posted.
type posted struct {
	kind, reason string
	count        int32
}

// postedAbout answers what the fake holds about one object, once every
// goroutine of the bubble, the recorder's among them, is idle.
func postedAbout(recorded *eventstest.Events, kind, name string) []posted {
	synctest.Wait()
	var out []posted
	for _, event := range recorded.About(kind, name) {
		out = append(out, posted{event.Type, event.Reason, event.Count})
	}
	return out
}

func held(kind string, status ConditionStatus, reason string) Condition {
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
		{"a new condition", nil, held(reachableConditionType, ConditionUnknown, reasonConnecting),
			[]posted{{events.TypeNormal, reasonConnecting, 1}}},
		{"a receiver that stops answering", []Condition{held(reachableConditionType, ConditionTrue, reasonConnected)},
			held(reachableConditionType, ConditionFalse, reasonUnreachable), []posted{{events.TypeWarning, reasonUnreachable, 1}}},
		{"a new message alone", []Condition{held(reachableConditionType, ConditionTrue, reasonConnected)},
			Condition{Type: reachableConditionType, Status: ConditionTrue, Reason: reasonConnected, Message: "another"}, nil},
		{"an input the receiver cannot report", []Condition{held(inputSelectedConditionType, ConditionTrue, reasonSessionInput)},
			held(inputSelectedConditionType, ConditionUnknown, reasonUnreachable), []posted{{events.TypeNormal, reasonUnreachable, 1}}},
		{"a bus in Listen", nil, held(conditionScanned, ConditionFalse, reasonListening),
			[]posted{{events.TypeNormal, reasonListening, 1}}},
		{"a bus whose polls nobody answers", []Condition{held(conditionScanned, ConditionUnknown, reasonScanning)},
			held(conditionScanned, ConditionFalse, reasonNoAnswer), []posted{{events.TypeWarning, reasonNoAnswer, 1}}},
		{"an adapter gone stale", []Condition{held(conditionJoined, ConditionTrue, "Joined")},
			held(conditionJoined, ConditionUnknown, reasonStale), []posted{{events.TypeWarning, reasonStale, 1}}},
		{"a power the TV did not confirm", nil, held(conditionPowerApplied, ConditionFalse, reasonUnconfirmed),
			[]posted{{events.TypeWarning, reasonUnconfirmed, 1}}},
		{"a wake a new power ended", []Condition{held(conditionWakeApplied, ConditionUnknown, reasonWaking)},
			held(conditionWakeApplied, ConditionFalse, reasonSuperseded), []posted{{events.TypeNormal, reasonSuperseded, 1}}},
		{"another Television in charge", nil, held(conditionInCharge, ConditionFalse, reasonAnotherInCharge),
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

// A cluster-scoped object's Events go in default, where the API server
// takes them.
func TestAnEventAboutATelevisionGoesInDefault(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		recorded := &eventstest.Events{}
		client := testAPIClient(t, recorded.Around(http.NotFoundHandler()))

		postTransitions(testRecorder(t, client), reference("Television", ObjectMeta{Name: "lounge"}), nil,
			[]Condition{held(conditionReachable, ConditionTrue, reasonAnswers)})
		synctest.Wait()

		list := recorded.List()
		mustMatch(t, len(list), 1)
		mustMatch(t, list[0].Metadata.Namespace, "default")
		mustMatch(t, list[0].InvolvedObject.APIVersion, equipmentAPIVersion)
		mustMatch(t, list[0].Message, "a message")
	})
}
