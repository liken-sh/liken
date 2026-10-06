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
// one object with one reason, in the order the operator wrote them.
func eventsAbout(a *fakeAPI, kind observatory.Kind, name, reason string) []string {
	var out []string
	for _, e := range a.recorded.About(kind.Name, name) {
		if e.Reason == reason {
			out = append(out, e.Reason+": "+e.Message)
		}
	}
	return out
}

// devicePhases answers each phase a device's status held, in the order
// the operator wrote them, from the first write after since.
func devicePhases(a *fakeAPI, kind observatory.Kind, name string, since int) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, e := range a.events {
		status, _ := e.object["status"].(map[string]any)
		if e.version > since && e.collection == kindCollection(kind) && e.object["metadata"].(map[string]any)["name"] == name && status != nil {
			out = append(out, status["phase"].(string))
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
					return slices.Contains(eventsAbout(w.api, c.kind, c.object, reasonPodCreated), c.event)
				})
				messages := readyMessages(w.api, kindCollection(c.kind), c.object)
				if !slices.Contains(messages, "Creating pod "+c.pod) {
					t.Errorf("no message names the pod's creation: %q", messages)
				}
				if got := eventsAbout(w.api, c.kind, c.object, reasonPodCreated); len(got) != 1 {
					t.Errorf("Events = %q, want one", got)
				}
				if !strings.Contains(w.logs.String(), c.log) {
					t.Errorf("no log line %q", c.log)
				}
			})
		})
	}
}

// A Ready reservation's runner creates an INDI server's pod again too,
// and records it the same way. Every driver on the new server starts
// disconnected, and the runner connects each device again.
func TestARecreatedServerPodIsNamedInAnEvent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		want := "PodCreated: Created pod east-telescope while Reservation east-tonight is Ready. Its drivers start disconnected, and the runner connects each device again."

		w.api.deleteNamed(podsCollection, "east-telescope")
		w.until(time.Minute, "the Event is not written", func() bool {
			return slices.Contains(eventsAbout(w.api, observatory.TelescopeKind, "east", reasonPodCreated), want)
		})

		if got := eventsAbout(w.api, observatory.TelescopeKind, "east", reasonPodCreated); len(got) != 1 {
			t.Errorf("Events = %q, want one", got)
		}
		if log := "Reservation east-tonight: created pod east-telescope for Telescope east"; !strings.Contains(w.logs.String(), log) {
			t.Errorf("no log line %q", log)
		}
	})
}

// While a Ready reservation's runner creates a device's pod again, the
// device is Starting, not Inventory: Inventory means that no
// reservation needs the device.
func TestADeviceWhosePodIsGoneIsStarting(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.api.mu.Lock()
		since := w.api.version
		w.api.mu.Unlock()
		w.api.refuse(2)

		w.api.deleteNamed(podsCollection, "east-main-camera")
		w.until(time.Minute, "the camera is not Connected again", func() bool {
			phases := devicePhases(w.api, observatory.CameraKind, "east-main", since)
			return len(phases) > 1 && phases[len(phases)-1] == string(observatory.DeviceConnected)
		})

		phases := devicePhases(w.api, observatory.CameraKind, "east-main", since)
		if slices.Contains(phases, string(observatory.DeviceInventory)) || !slices.Contains(phases, string(observatory.DeviceStarting)) {
			t.Errorf("the camera's phases were %q, want Starting and never Inventory", phases)
		}
	})
}
