package main

// The kubelet can end a pod for a reason that is not the pod's own
// program: an eviction, a rejection at admission, or a node's
// shutdown. The pod then stays Failed, or Succeeded when its
// containers exited zero at a shutdown, and its restartPolicy does not
// start it again. These tests end each kind of pod in that way, during
// an activation and while a reservation is Ready, and check that the
// operator creates the pod again.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// endPod gives a pod a final phase as the kubelet does when it evicts
// the pod or shuts its node down. It also stops holding the pod Pending
// (holdPending), so the pod that replaces it becomes Ready.
func (a *fakeAPI) endPod(name, phase string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.pending, name)
	held, ok := a.objects[podsCollection][name]
	if !ok {
		return
	}
	next := clone(held)
	next["status"] = map[string]any{
		"phase": phase, "reason": "Evicted", "message": "The node was low on resource: memory.",
		"conditions": []any{
			map[string]any{"type": "DisruptionTarget", "status": "True", "reason": "TerminationByKubelet"},
			map[string]any{"type": "Ready", "status": "False"},
		},
	}
	a.store(podsCollection, name, next, "MODIFIED")
}

// podState returns the UID and the phase of a pod, or two empty
// strings when the pod does not exist.
func podState(a *fakeAPI, name string) (uid, phase string) {
	p, ok := a.object(podsCollection, name)
	if !ok {
		return "", ""
	}
	status, _ := p["status"].(map[string]any)
	phase, _ = status["phase"].(string)
	return p["metadata"].(map[string]any)["uid"].(string), phase
}

// The pods of east-tonight's telescope, each with the kind and name of
// the object that its PodCreated Event is about.
var (
	telescopeServer = endedPod{"east-telescope", observatory.TelescopeKind, "east"}
	mainCamera      = endedPod{"east-main-camera", observatory.CameraKind, "east-main"}
	eastGuider      = endedPod{"east-guider", observatory.GuiderKind, "east"}
)

type endedPod struct {
	name   string
	kind   observatory.Kind
	object string
}

// created returns the PodCreated Events about the pod's object.
func (p endedPod) created(w *world) []string {
	return eventsAbout(w.api, p.kind, p.object, reasonPodCreated)
}

func TestAPodThatTheKubeletEndedIsCreatedAgainWhileReady(t *testing.T) {
	cases := []struct {
		pod   endedPod
		phase string
		event string
	}{
		{telescopeServer, "Failed", "PodCreated: Created pod east-telescope while Reservation east-tonight is Ready. Its drivers start disconnected, and the runner connects each device again"},
		{telescopeServer, "Succeeded", "PodCreated: Created pod east-telescope while Reservation east-tonight is Ready. Its drivers start disconnected, and the runner connects each device again"},
		{mainCamera, "Failed", "PodCreated: Created pod east-main-camera while Reservation east-tonight is Ready"},
		{mainCamera, "Succeeded", "PodCreated: Created pod east-main-camera while Reservation east-tonight is Ready"},
		{eastGuider, "Failed", "PodCreated: Created pod east-guider while Reservation east-tonight is Ready. The new PHD2 starts idle and not calibrated"},
		{eastGuider, "Succeeded", "PodCreated: Created pod east-guider while Reservation east-tonight is Ready. The new PHD2 starts idle and not calibrated"},
	}
	for _, c := range cases {
		t.Run(c.pod.name+" "+c.phase, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := readyWorld(t)
				ended, _ := podState(w.api, c.pod.name)

				w.api.endPod(c.pod.name, c.phase)
				w.until(time.Minute, "no PodCreated Event was posted", func() bool {
					return slices.Contains(c.pod.created(w), c.event)
				})

				if uid, phase := podState(w.api, c.pod.name); uid == ended || phase != "Running" {
					t.Errorf("pod %s has UID %q and phase %q, want a new pod that is Running in place of %q", c.pod.name, uid, phase, ended)
				}
				if got := c.pod.created(w); len(got) != 1 {
					t.Errorf("Events = %q, want one", got)
				}
				w.phase("east-tonight", observatory.ReservationReady, time.Minute)
			})
		})
	}
}

// The kubelet ends a pod while an activation step waits for it: the
// server's pod while the step waits for the INDI connection, the
// camera's pod while it waits for the pod to be Ready, and the guider's
// pod while it waits for PHD2. The step creates the pod again and goes
// on waiting, and the reservation becomes Ready.
func TestAPodThatTheKubeletEndsDuringAnActivationWaitIsCreatedAgain(t *testing.T) {
	cases := []struct {
		pod   endedPod
		phase string
		event string
	}{
		{telescopeServer, "Failed", "PodCreated: Created pod east-telescope again during the activation of Reservation east-tonight"},
		{telescopeServer, "Succeeded", "PodCreated: Created pod east-telescope again during the activation of Reservation east-tonight"},
		{mainCamera, "Failed", "PodCreated: Created pod east-main-camera again during the activation of Reservation east-tonight"},
		{mainCamera, "Succeeded", "PodCreated: Created pod east-main-camera again during the activation of Reservation east-tonight"},
		{eastGuider, "Failed", "PodCreated: Created pod east-guider again during the activation of Reservation east-tonight"},
		{eastGuider, "Succeeded", "PodCreated: Created pod east-guider again during the activation of Reservation east-tonight"},
	}
	for _, c := range cases {
		t.Run(c.pod.name+" "+c.phase, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.api.holdPending(c.pod.name)
				w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
				w.until(10*time.Minute, "pod "+c.pod.name+" was not created", func() bool {
					_, phase := podState(w.api, c.pod.name)
					return phase == "Pending"
				})
				ended, _ := podState(w.api, c.pod.name)

				w.api.endPod(c.pod.name, c.phase)
				w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)

				if uid, phase := podState(w.api, c.pod.name); uid == ended || phase != "Running" {
					t.Errorf("pod %s has UID %q and phase %q, want a new pod that is Running in place of %q", c.pod.name, uid, phase, ended)
				}
				if got := c.pod.created(w); !slices.Equal(got, []string{c.event}) {
					t.Errorf("Events = %q, want only %q", got, c.event)
				}
			})
		})
	}
}

// The operator stops while an activation waits for a pod, and the
// kubelet ends the pod before the operator starts again. The step that
// resumes creates the pod again, and the reservation becomes Ready.
func TestAPodThatTheKubeletEndedIsCreatedAgainWhenActivationResumes(t *testing.T) {
	cases := []struct {
		pod   endedPod
		phase string
	}{
		{telescopeServer, "Failed"},
		{telescopeServer, "Succeeded"},
		{mainCamera, "Failed"},
		{mainCamera, "Succeeded"},
		{eastGuider, "Failed"},
		{eastGuider, "Succeeded"},
	}
	for _, c := range cases {
		t.Run(c.pod.name+" "+c.phase, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.api.holdPending(c.pod.name)
				w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
				w.until(10*time.Minute, "pod "+c.pod.name+" was not created", func() bool {
					_, phase := podState(w.api, c.pod.name)
					return phase == "Pending"
				})
				ended, _ := podState(w.api, c.pod.name)

				w.halt()
				w.api.endPod(c.pod.name, c.phase)
				w.start()
				w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)

				if uid, phase := podState(w.api, c.pod.name); uid == ended || phase != "Running" {
					t.Errorf("pod %s has UID %q and phase %q, want a new pod that is Running in place of %q", c.pod.name, uid, phase, ended)
				}
			})
		})
	}
}

// rejectPods makes the kubelet reject each pod of the name that the
// operator creates from now on, as a node without enough CPU does.
func (a *fakeAPI) rejectPods(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rejected[name] = true
}

// podCreates returns the time of each create of a pod, oldest first.
func podCreates(a *fakeAPI, name string) []time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []time.Time
	for _, e := range a.events {
		metadata := e.object["metadata"].(map[string]any)
		if e.collection == podsCollection && e.kind == "ADDED" && metadata["name"] == name {
			created, _ := time.Parse(time.RFC3339, metadata["creationTimestamp"].(string))
			out = append(out, created)
		}
	}
	return out
}

// The camera's claim keeps its pod on one node, and the kubelet there
// rejects each new pod at once. The operator replaces each rejected pod
// no earlier than its creation plus a wait of 10 seconds for a count of
// zero, doubled for each count, to a limit of 5 minutes. The camera's
// first pod ran for longer than its wait, so it is replaced at once,
// and that first replacement has a count of 1, so it waits 20 seconds.
// The operator posts one PodCreated Event for the loop.
func TestTheReplacementOfAPodThatTheKubeletRejectsWaitsLongerEachTime(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.api.rejectPods(mainCamera.name)
		w.api.endPod(mainCamera.name, "Failed")
		time.Sleep(20 * time.Minute)
		synctest.Wait()

		creates := podCreates(w.api, mainCamera.name)
		var gaps []time.Duration
		for i := 2; i < len(creates); i++ {
			gaps = append(gaps, creates[i].Sub(creates[i-1]))
		}
		want := []time.Duration{20 * time.Second, 40 * time.Second, 80 * time.Second, 160 * time.Second, 5 * time.Minute, 5 * time.Minute}
		if len(gaps) < len(want) || !slices.Equal(gaps[:len(want)], want) {
			t.Errorf("the times between the creates of the rejected pods are %v, want %v first", gaps, want)
		}
		if got := mainCamera.created(w); len(got) != 1 {
			t.Errorf("Events = %q, want one", got)
		}
	})
}

// staleEndedPod makes a pod one that the kubelet ended just now after
// three earlier pods of its name, and gives it the spec digest of
// another spec, as a pod has after a person changed what the operator
// builds.
func (a *fakeAPI) staleEndedPod(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	next := clone(a.objects[podsCollection][name])
	metadata := next["metadata"].(map[string]any)
	metadata["creationTimestamp"] = time.Now().UTC().Format(time.RFC3339)
	metadata["annotations"].(map[string]any)[annotationTries] = "3"
	metadata["annotations"].(map[string]any)[annotationSpec] = "0000000000000000"
	next["status"] = map[string]any{"phase": "Failed", "reason": "OutOfcpu"}
	a.store(podsCollection, name, next, "MODIFIED")
}

// An ended pod whose spec differs from the one the operator builds is
// replaced at once, without the wait of its count, and the new pod has
// no count, because the change can fix what the kubelet refused.
func TestAnEndedPodWithAnotherSpecIsReplacedAtOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.halt()
		w.api.staleEndedPod(mainCamera.name)
		ended, _ := podState(w.api, mainCamera.name)
		w.start()

		w.until(10*time.Second, "the camera's pod was not replaced", func() bool {
			uid, phase := podState(w.api, mainCamera.name)
			return uid != ended && phase == "Running"
		})
		p, _ := w.api.object(podsCollection, mainCamera.name)
		if tries, counted := p["metadata"].(map[string]any)["annotations"].(map[string]any)[annotationTries]; counted {
			t.Errorf("the new pod has the count %v, want none", tries)
		}
	})
}
