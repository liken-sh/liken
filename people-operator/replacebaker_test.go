package main

import (
	"slices"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
)

// kubeletEnds holds the pod status of each way the kubelet ends a
// baker pod for a cause outside the bake command.
var kubeletEnds = []struct {
	name   string
	status map[string]any
}{
	{"an eviction", map[string]any{
		"reason":     "Evicted",
		"message":    "The node was low on resource: memory.",
		"conditions": []any{map[string]any{"type": "DisruptionTarget", "status": "True", "reason": "TerminationByKubelet"}},
	}},
	{"a node shutdown", map[string]any{"reason": "Terminated", "message": "Pod was terminated in response to imminent node shutdown."}},
	{"a disruption with no pod reason", map[string]any{
		"conditions": []any{map[string]any{"type": "DisruptionTarget", "status": "True", "reason": "PreemptionByScheduler"}},
	}},
}

// onlyBakerPod returns the one baker pod of ada in media, and fails the
// test when there is not exactly one.
func onlyBakerPod(t *testing.T, w *world) pod {
	t.Helper()
	pods := w.api.bakerPodsOf(t, "media", "ada")
	if len(pods) != 1 {
		t.Fatalf("ada has %d baker pods, want 1: %+v", len(pods), pods)
	}
	return pods[0]
}

// A baker pod that the kubelet ended records no outcome. It stays for
// the first wait, so a person can read its status. Then the operator
// creates the next pod, and that pod's result becomes the status.
func TestABakerPodThatTheKubeletEndedIsCreatedAgain(t *testing.T) {
	for _, c := range kubeletEnds {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.api.putPerson(ada("claim://media/pictures/ada.png"))
				w.waitForIdle()

				w.api.endPod("media", bakerPodName("ada"), c.status)
				w.waitForIdle()
				if ended := onlyBakerPod(t, w); ended.Status.Phase != podFailed {
					t.Errorf("the baker pod has the phase %q during the wait, want the ended pod, Failed", ended.Status.Phase)
				}

				time.Sleep(firstRetryWait)
				w.waitForIdle()
				if ready := w.api.person(t, "ada").Status.ready(); ready.Reason != "" {
					t.Errorf("AvatarReady = %+v, want no condition while the next baker pod runs", ready)
				}
				next := onlyBakerPod(t, w)
				if next.Status.Phase != "Pending" || next.Metadata.Annotations[triesKey] != "1" {
					t.Fatalf("the baker pod has the phase %q and the tries %q, want Pending and 1", next.Status.Phase, next.Metadata.Annotations[triesKey])
				}

				w.api.finishPod("media", next.Metadata.Name, podSucceeded, bakedLine(t, solid(t, 64, 64, green)))
				w.waitForIdle()

				if ready := w.api.person(t, "ada").Status.ready(); ready.Status != "True" || ready.Reason != reasonBaked {
					t.Errorf("AvatarReady = %+v, want True, Baked", ready)
				}
				want := []posted{{events.TypeWarning, reasonPodRecreated, 1}, {events.TypeNormal, reasonBaked, 1}}
				if got := w.postedAbout("ada"); !slices.Equal(got, want) {
					t.Errorf("the Events about ada = %+v, want %+v", got, want)
				}
			})
		})
	}
}

// A recheck's baker pod that the kubelet ended is created again too,
// and the last picture keeps AvatarReady at Baked meanwhile.
func TestARecheckThatTheKubeletEndedBakesAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.putPerson(ada("claim://media/pictures/ada.png"))
		w.waitForIdle()
		w.api.finishPod("media", bakerPodName("ada"), podSucceeded, bakedLine(t, solid(t, 64, 64, green)))
		w.waitForIdle()

		time.Sleep(recheckInterval)
		w.waitForIdle()
		w.api.endPod("media", bakerPodName("ada"), kubeletEnds[0].status)
		time.Sleep(firstRetryWait)
		w.waitForIdle()

		if ready := w.api.person(t, "ada").Status.ready(); ready.Status != "True" || ready.Reason != reasonBaked {
			t.Errorf("AvatarReady = %+v, want True, Baked while the next baker pod runs", ready)
		}
		if next := onlyBakerPod(t, w); next.Status.Phase != "Pending" {
			t.Errorf("the baker pod has the phase %q, want a new Pending pod", next.Status.Phase)
		}
	})
}

// A claim on a node-local class pins a baker pod to one node, so the
// kubelet can refuse each next pod at admission. Each next pod waits
// twice as long from the creation of the pod before it, to a limit of
// 5 minutes, and only the first recreate posts an Event.
func TestTheWaitDoublesWhileTheKubeletRefusesEachBakerPod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.rejectPods(map[string]any{"reason": "NodeAffinity", "message": "Predicate NodeAffinity failed"})
		w.api.putPerson(ada("claim://media/pictures/ada.png"))
		time.Sleep(16 * time.Minute)
		w.waitForIdle()

		created := w.api.createTimes()
		var gaps []time.Duration
		for i := 1; i < len(created); i++ {
			gaps = append(gaps, created[i].Sub(created[i-1]))
		}
		want := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 160 * time.Second, 5 * time.Minute, 5 * time.Minute}
		if !slices.Equal(gaps, want) {
			t.Errorf("the waits between the baker pods are %v, want %v", gaps, want)
		}
		if got, want := w.postedAbout("ada"), []posted{{events.TypeWarning, reasonPodRecreated, 1}}; !slices.Equal(got, want) {
			t.Errorf("the Events about ada = %+v, want %+v", got, want)
		}
	})
}

// endedBaker returns ada's baker pod as the kubelet left it after an
// eviction an hour ago, so its wait is over.
func endedBaker(t *testing.T) pod {
	p := ada("claim://media/pictures/ada.png")
	mount, _ := claimMount(mustParse(t, p.Spec.Avatar), "")
	ended := buildBakerPod(&p, mount, operatorImage)
	ended.Metadata.CreationTimestamp = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	ended.Status = podStatus{Phase: podFailed, Reason: "Evicted"}
	return ended
}

// An operator that stops between the steps of a replacement leaves
// the ended pod, or the ended pod and its next pod. The operator that
// follows finishes the replacement, so a recheck's bake is not lost
// until the next recheck. Only a pod that the operator created posts
// an Event.
func TestAnOperatorFinishesAReplacementThatAnEarlierOneStarted(t *testing.T) {
	cases := []struct {
		name       string
		withNext   bool
		wantEvents []posted
	}{
		{"before the create", false, []posted{{events.TypeNormal, reasonBaked, 1}, {events.TypeWarning, reasonPodRecreated, 1}}},
		{"after the create", true, []posted{{events.TypeNormal, reasonBaked, 1}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.api.putPerson(ada("claim://media/pictures/ada.png"))
				w.waitForIdle()
				w.api.finishPod("media", bakerPodName("ada"), podSucceeded, bakedLine(t, solid(t, 64, 64, green)))
				w.waitForIdle()

				// The earlier operator created the next pod before it
				// would have deleted the ended one, so the watch
				// delivers the next pod first.
				if c.withNext {
					next := endedBaker(t)
					next.Metadata.Name = tryPodName("ada", 1)
					next.Metadata.Annotations[triesKey] = "1"
					next.Metadata.CreationTimestamp = time.Now().UTC().Format(time.RFC3339)
					next.Status = podStatus{Phase: "Pending"}
					w.api.putPod(next)
				}
				w.api.putPod(endedBaker(t))
				w.waitForIdle()

				if next := onlyBakerPod(t, w); next.Status.Phase != "Pending" || next.Metadata.Annotations[triesKey] != "1" {
					t.Errorf("the baker pod has the phase %q and the tries %q, want Pending and 1", next.Status.Phase, next.Metadata.Annotations[triesKey])
				}
				if got := w.postedAbout("ada"); !slices.Equal(got, c.wantEvents) {
					t.Errorf("the Events about ada = %+v, want %+v", got, c.wantEvents)
				}
			})
		})
	}
}

// When the create of the next pod gets 409, because a pod outside the
// watch holds its name, the operator posts no Event, because it
// created nothing.
func TestARefusedCreatePostsNoRecreate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.putPerson(ada("claim://media/pictures/ada.png"))
		w.waitForIdle()
		ended := endedBaker(t)
		holder := pod{Metadata: podMeta{Name: tryPodName("ada", 1), Namespace: "media"}}
		w.api.putPod(holder)
		w.api.putPod(ended)
		w.waitForIdle()

		if got := w.postedAbout("ada"); slices.Contains(got, posted{events.TypeWarning, reasonPodRecreated, 1}) {
			t.Errorf("the Events about ada = %+v, want no PodRecreated, because no pod was created", got)
		}
	})
}

// stamp formats a time as the API server writes a timestamp, cut to
// whole seconds.
func stamp(at time.Time) string { return at.UTC().Format(time.RFC3339) }

// The recreate Event posts for the first pod of a bake, and for a pod
// that ran for longer than maxRetryWait before the kubelet ended it.
// The age of a pod at its replacement is no measure: a pod that the
// kubelet refused at admission never ran, however late the pass
// replaces it.
func TestTheRecreateEventPostsForTheFirstEndAndForALongRun(t *testing.T) {
	cases := []struct {
		name      string
		tries     int
		age       time.Duration
		status    func(created time.Time) podStatus
		wantEvent bool
	}{
		{"the first pod", 0, time.Hour, func(time.Time) podStatus {
			return podStatus{Phase: podFailed, Reason: "NodeAffinity"}
		}, true},
		{"a seventh try refused at admission 5m1s ago", 6, 5*time.Minute + time.Second, func(time.Time) podStatus {
			return podStatus{Phase: podFailed, Reason: "NodeAffinity"}
		}, false},
		{"a seventh try evicted after 6 minutes", 6, time.Hour, func(created time.Time) podStatus {
			return podStatus{Phase: podFailed, Reason: "Evicted", Conditions: []podCondition{
				{Type: "DisruptionTarget", Status: "True", LastTransitionTime: stamp(created.Add(6 * time.Minute))},
			}}
		}, true},
		{"a seventh try whose container ended after 6 minutes", 6, time.Hour, func(created time.Time) podStatus {
			return podStatus{Phase: podFailed, Reason: "Terminated", ContainerStatuses: []containerStatus{
				{State: containerState{Terminated: &terminatedState{FinishedAt: stamp(created.Add(6 * time.Minute))}}},
			}}
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.api.putPerson(ada("claim://media/pictures/ada.png"))
				w.waitForIdle()
				w.api.finishPod("media", bakerPodName("ada"), podSucceeded, bakedLine(t, solid(t, 64, 64, green)))
				w.waitForIdle()

				ended := endedBaker(t)
				created := time.Now().Add(-c.age)
				ended.Metadata.Name = tryPodName("ada", c.tries)
				ended.Metadata.Annotations[triesKey] = strconv.Itoa(c.tries)
				ended.Metadata.CreationTimestamp = stamp(created)
				ended.Status = c.status(created)
				w.api.putPod(ended)
				w.waitForIdle()

				posted := slices.Contains(w.postedAbout("ada"), posted{events.TypeWarning, reasonPodRecreated, 1})
				if posted != c.wantEvent {
					t.Errorf("PodRecreated posted: %v, want %v", posted, c.wantEvent)
				}
			})
		})
	}
}

// A changed spec.avatar or check-avatar annotation means that the ended
// pod's bake is no longer the one wanted. The operator starts the new
// bake at once, as the first pod of its bake, with no wait and no
// count, because the person may have changed the Person to fix what the
// kubelet refused.
func TestAChangedPersonReplacesAnEndedBakerPodAtOnce(t *testing.T) {
	cases := []struct {
		name    string
		changed person
	}{
		{"a new spec.avatar", ada("claim://media/pictures/ada-2.png")},
		{"a new check-avatar", withAnnotation(ada("claim://media/pictures/ada.png"), "again")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.api.rejectPods(map[string]any{"reason": "NodeAffinity"})
				w.api.putPerson(ada("claim://media/pictures/ada.png"))
				time.Sleep(time.Minute)
				w.waitForIdle()
				w.api.rejectPods(nil)

				w.api.putPerson(c.changed)
				w.waitForIdle()

				next := onlyBakerPod(t, w)
				if next.Status.Phase != "Pending" || next.Metadata.Annotations[triesKey] != "" || next.Metadata.Annotations[bakerSourceKey] != c.changed.Spec.Avatar {
					t.Errorf("the baker pod has the phase %q, the tries %q, and the source %q, want Pending, no count, and %q",
						next.Status.Phase, next.Metadata.Annotations[triesKey], next.Metadata.Annotations[bakerSourceKey], c.changed.Spec.Avatar)
				}
				if ready := w.api.person(t, "ada").Status.ready(); ready.Reason != "" {
					t.Errorf("AvatarReady = %+v, want no condition while the new bake runs", ready)
				}
			})
		})
	}
}
