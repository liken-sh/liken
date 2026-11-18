package main

// When a pod is deleted during a Ready reservation, the runner creates
// it again, and the status, an Event, and a log line say so.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// readyMessages returns each message that the operator wrote to an
// object's Ready condition, oldest first.
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

// eventsAbout returns "reason: message" for each Event with the given
// reason about one object, oldest first.
func eventsAbout(a *fakeAPI, kind observatory.Kind, name, reason string) []string {
	var out []string
	for _, e := range a.recorded.About(kind.Name, name) {
		if e.Reason == reason {
			out = append(out, e.Reason+": "+e.Message)
		}
	}
	return out
}

// devicePhases returns each phase that the operator wrote to a
// device's status after the fake API server's version since, oldest
// first.
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
			"PodCreated: Created pod east-guider while Reservation east-tonight is Ready. The new PHD2 starts idle and not calibrated",
			"Reservation east-tonight: created pod east-guider for Guider east",
		},
		{
			"a device", "east-main-camera", observatory.CameraKind, "east-main",
			"PodCreated: Created pod east-main-camera while Reservation east-tonight is Ready",
			"Reservation east-tonight: created pod east-main-camera for Camera east-main",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := readyWorld(t)
				// The fake API server refuses the next two creates, so the
				// pod stays missing across more than one status write.
				w.api.refuse(2)
				w.api.deleteNamed(podsCollection, c.pod)
				w.until(time.Minute, "no PodCreated Event was posted", func() bool {
					return slices.Contains(eventsAbout(w.api, c.kind, c.object, reasonPodCreated), c.event)
				})
				messages := readyMessages(w.api, kindCollection(c.kind), c.object)
				if !slices.Contains(messages, "Creating pod "+c.pod) {
					t.Errorf("the Ready messages are %q, want one that says Creating pod %s", messages, c.pod)
				}
				if got := eventsAbout(w.api, c.kind, c.object, reasonPodCreated); len(got) != 1 {
					t.Errorf("Events = %q, want one", got)
				}
				if !strings.Contains(w.logs.String(), c.log) {
					t.Errorf("the log lacks the line %q", c.log)
				}
			})
		})
	}
}

func TestARecreatedServerPodIsNamedInAnEvent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		want := "PodCreated: Created pod east-telescope while Reservation east-tonight is Ready. Its drivers start disconnected, and the runner connects each device again"

		w.api.deleteNamed(podsCollection, "east-telescope")
		w.until(time.Minute, "no PodCreated Event was posted", func() bool {
			return slices.Contains(eventsAbout(w.api, observatory.TelescopeKind, "east", reasonPodCreated), want)
		})

		if got := eventsAbout(w.api, observatory.TelescopeKind, "east", reasonPodCreated); len(got) != 1 {
			t.Errorf("Events = %q, want one", got)
		}
		if log := "Reservation east-tonight: created pod east-telescope for Telescope east"; !strings.Contains(w.logs.String(), log) {
			t.Errorf("the log lacks the line %q", log)
		}
	})
}

// A device whose pod the runner creates again is Starting, not Idle,
// because Idle means that no reservation needs the device.
func TestADeviceWhosePodIsGoneIsStarting(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.api.mu.Lock()
		since := w.api.version
		w.api.mu.Unlock()
		w.api.refuse(2)

		w.api.deleteNamed(podsCollection, "east-main-camera")
		w.until(time.Minute, "the camera did not become Connected again", func() bool {
			phases := devicePhases(w.api, observatory.CameraKind, "east-main", since)
			return len(phases) > 1 && phases[len(phases)-1] == string(observatory.DeviceConnected)
		})

		phases := devicePhases(w.api, observatory.CameraKind, "east-main", since)
		if slices.Contains(phases, string(observatory.DeviceIdle)) || !slices.Contains(phases, string(observatory.DeviceStarting)) {
			t.Errorf("the camera's phases were %q, want Starting and never Idle", phases)
		}
	})
}
