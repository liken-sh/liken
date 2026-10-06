package main

// A pod that a Ready reservation's runner creates again: the status
// says what the runner does while the pod is gone, and an Event and a
// log line say that it created the pod.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// readyMessages answers each message of an object's Ready condition,
// in the order the operator wrote them.
func readyMessages(a *fakeAPI, collection, name string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, e := range a.events {
		if e.collection != collection || e.object["metadata"].(map[string]any)["name"] != name {
			continue
		}
		status, _ := e.object["status"].(map[string]any)
		conditions, _ := status["conditions"].([]any)
		for _, c := range conditions {
			c := c.(map[string]any)
			if c["type"] == string(observatory.ConditionReady) {
				out = append(out, c["message"].(string))
			}
		}
	}
	return out
}

// eventsAbout answers the reason and the message of each Event about
// one object, in the order the operator wrote them.
func eventsAbout(a *fakeAPI, kind observatory.Kind, name string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, e := range a.events {
		involved, _ := e.object["involvedObject"].(map[string]any)
		if e.collection == eventsCollection && e.kind == "ADDED" && involved["kind"] == kind.Name && involved["name"] == name {
			out = append(out, e.object["reason"].(string)+": "+e.object["message"].(string))
		}
	}
	return out
}

func TestARecreatedPodIsNamedInTheStatusAndAnEvent(t *testing.T) {
	cases := []struct {
		name, pod  string
		kind       observatory.Kind
		object     string
		event, log string
	}{
		{
			"the guider", "east-guider", observatory.GuiderKind, "east",
			"PodCreated: Created pod east-guider while Reservation east-tonight is Ready. The new PHD2 starts idle and not calibrated.",
			"Reservation east-tonight: created pod east-guider for Guider east",
		},
		{
			"a device", "east-main-camera", observatory.CameraKind, "east-main",
			"PodCreated: Created pod east-main-camera while Reservation east-tonight is Ready.",
			"Reservation east-tonight: created pod east-main-camera for Camera east-main",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := readyWorld(t)
				// The API server refuses the first creates, so the pod
				// stays gone for a few status windows.
				w.api.refuse(2)
				w.api.deleteNamed(podsCollection, c.pod)
				w.until(time.Minute, "the Event is not written", func() bool {
					return slices.Contains(eventsAbout(w.api, c.kind, c.object), c.event)
				})
				messages := readyMessages(w.api, kindCollection(c.kind), c.object)
				if !slices.Contains(messages, "Creating pod "+c.pod) {
					t.Errorf("no message names the pod's creation: %q", messages)
				}
				if got := eventsAbout(w.api, c.kind, c.object); len(got) != 1 {
					t.Errorf("Events = %q, want one", got)
				}
				if !strings.Contains(w.logs.String(), c.log) {
					t.Errorf("no log line %q", c.log)
				}
			})
		})
	}
}
