package main

// Tests of the outbox: the claim that a reservation names, which the
// telescope's imaging cameras mount and write their frames to.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

const volumeClaimsCollection = "/api/v1/namespaces/" + testNamespace + "/persistentvolumeclaims"

// putClaim creates a PersistentVolumeClaim in the example's namespace.
func (w *world) putClaim(name string) {
	w.api.put(volumeClaimsCollection, map[string]any{
		"apiVersion": "v1", "kind": "PersistentVolumeClaim",
		"metadata": map[string]any{"name": name},
		"spec":     map[string]any{"accessModes": []any{"ReadWriteOnce"}},
	})
}

// outboxWorld runs the example with a claim named frames. It starts
// east-tonight Ready with spec.outbox set to the outbox argument, or
// with no outbox when the argument is "".
func outboxWorld(t *testing.T, outbox string) *world {
	w := startWorld(t)
	w.putClaim("frames")
	spec := map[string]any{"telescope": "east", "holder": "desktop"}
	if outbox != "" {
		spec["outbox"] = map[string]any{"claimName": outbox}
	}
	w.reserve("east-tonight", spec)
	w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
	return w
}

// mountedOutbox describes how a pod mounts an outbox: the claim, where
// the driver sees it, and the group that the kubelet gives the volume.
// It returns "" for a pod with no outbox and no pod security context.
func (w *world) mountedOutbox(name string) string {
	w.t.Helper()
	p, ok := decode[pod](w.t, w.api, podsCollection, name)
	if !ok {
		w.t.Fatalf("the Pod %s is not in the API server", name)
	}
	var parts []string
	for _, v := range p.Spec.Volumes {
		if v.PersistentVolumeClaim != nil {
			parts = append(parts, "claim "+v.PersistentVolumeClaim.ClaimName)
		}
	}
	for _, m := range p.Spec.Containers[0].VolumeMounts {
		if m.Name == outboxVolume {
			parts = append(parts, "at "+m.MountPath)
		}
	}
	if s := p.Spec.SecurityContext; s != nil {
		parts = append(parts, fmt.Sprintf("fsGroup %d %s", *s.FSGroup, s.FSGroupChangePolicy))
	}
	return strings.Join(parts, ", ")
}

// Every Camera of the telescope mounts the outbox at /outbox, except
// the one that a Guider guides with, and a device of another kind
// mounts nothing. With no outbox, no pod changes.
func TestOnlyAnImagingCameraMountsTheOutbox(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, outbox, pod, want string
	}{
		{"the imaging camera", "frames", "east-main-camera", "claim frames, at /outbox, fsGroup 1000 OnRootMismatch"},
		{"the guide camera", "frames", "east-guide-camera", ""},
		{"a focuser", "frames", "east-focuser", ""},
		{"the imaging camera with no outbox", "", "east-main-camera", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := outboxWorld(t, c.outbox)
				if got := w.mountedOutbox(c.pod); got != c.want {
					t.Errorf("%s mounts %q, want %q", c.pod, got, c.want)
				}
			})
		})
	}
}

// uploads returns the changes to the upload properties that clients
// sent, in order.
func (w *world) uploads() []string {
	var out []string
	for _, change := range w.indi.changes() {
		if strings.Contains(change, ".UPLOAD_") {
			out = append(out, change)
		}
	}
	return out
}

// Configure points the imaging camera's driver at the outbox, in
// UPLOAD_BOTH mode, and keeps the driver's prefix. It changes no upload
// setting of the guide camera, or of any camera when the reservation
// has no outbox.
func TestConfigurePointsTheImagingCameraAtTheOutbox(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, outbox string
		want         []string
	}{
		{"an outbox", "frames", []string{
			"east-telescope CCD Simulator.UPLOAD_SETTINGS UPLOAD_DIR=/outbox/east-main UPLOAD_PREFIX=IMAGE_XXX",
			"east-telescope CCD Simulator.UPLOAD_MODE UPLOAD_CLIENT=Off UPLOAD_LOCAL=Off UPLOAD_BOTH=On",
		}},
		{"no outbox", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := outboxWorld(t, c.outbox)
				if got := w.uploads(); !slices.Equal(got, c.want) {
					t.Errorf("upload changes = %q, want %q", got, c.want)
				}
			})
		})
	}
}

// With no Guider, both cameras of the telescope image, and each driver
// writes into the directory of its own Camera's name, so two drivers
// never choose a file name in one directory.
func TestEachImagingCameraWritesIntoItsOwnDirectory(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.deleteNamed(kindCollection(observatory.GuiderKind), "east")
		w.putClaim("frames")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "outbox": map[string]any{"claimName": "frames"}})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)

		var got []string
		for _, change := range w.uploads() {
			if strings.Contains(change, ".UPLOAD_SETTINGS ") {
				got = append(got, change)
			}
		}
		slices.Sort(got)
		want := []string{
			"east-telescope CCD Simulator.UPLOAD_SETTINGS UPLOAD_DIR=/outbox/east-main UPLOAD_PREFIX=IMAGE_XXX",
			"east-telescope Guide Simulator.UPLOAD_SETTINGS UPLOAD_DIR=/outbox/east-guide UPLOAD_PREFIX=IMAGE_XXX",
		}
		if !slices.Equal(got, want) {
			t.Errorf("upload settings = %q, want %q", got, want)
		}
	})
}

// A reservation whose outbox names a claim that does not exist fails
// PowerOn before the telescope's server starts, and the summary names
// the claim and its namespace. When the person creates the claim and
// asks for a retry, the session starts with the outbox.
func TestAnOutboxThatDoesNotExistFailsPowerOn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "outbox": map[string]any{"claimName": "nightly"}})
		r := w.phase("east-tonight", observatory.ReservationFailed, 15*time.Minute)

		want := "the PersistentVolumeClaim nightly that spec.outbox.claimName names is not in the namespace observatory"
		if step := stepOf(r, observatory.StepPowerOn); step.State != observatory.StepFailed || !strings.Contains(step.Summary, want) {
			t.Errorf("PowerOn = %+v, want Failed with %q", step, want)
		}
		if pods := w.podsOf("east-telescope"); len(pods) > 0 {
			t.Errorf("east runs %v, want no pods", pods)
		}

		w.putClaim("nightly")
		w.retry("east-tonight")
		w.phase("east-tonight", observatory.ReservationReady, 15*time.Minute)
		if got, want := w.mountedOutbox("east-main-camera"), "claim nightly, at /outbox, fsGroup 1000 OnRootMismatch"; got != want {
			t.Errorf("east-main-camera mounts %q, want %q", got, want)
		}
	})
}

// An edit of spec.outbox during the session takes effect at the next
// session: the camera's pod keeps its claim, and so does a pod that an
// eviction deletes after the edit.
func TestAnOutboxEditWaitsForTheNextSession(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		evict bool
	}{
		{"an edit", false},
		{"an edit, then an eviction", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := outboxWorld(t, "frames")
				w.putClaim("other")
				before, _ := w.api.object(podsCollection, "east-main-camera")
				w.amend(observatory.ReservationKind, "east-tonight", func(spec map[string]any) {
					spec["outbox"] = map[string]any{"claimName": "other"}
				})
				w.settle()
				if c.evict {
					w.api.deleteNamed(podsCollection, "east-main-camera")
					w.until(time.Minute, "the camera's pod is not back", func() bool {
						p, ok := w.api.object(podsCollection, "east-main-camera")
						return ok && podUID(p) != podUID(before)
					})
				}
				w.settle()

				after, _ := w.api.object(podsCollection, "east-main-camera")
				if !c.evict && podUID(after) != podUID(before) {
					t.Errorf("the camera's pod was replaced")
				}
				if got, want := w.mountedOutbox("east-main-camera"), "claim frames, at /outbox, fsGroup 1000 OnRootMismatch"; got != want {
					t.Errorf("east-main-camera mounts %q, want %q", got, want)
				}
			})
		})
	}
}

// holdUnschedulable keeps a pod Pending and sets the condition that the
// scheduler sets when no node fits the pod.
func (w *world) holdUnschedulable(name, message string) {
	w.until(time.Minute, "no pod "+name, func() bool {
		_, ok := w.api.object(podsCollection, name)
		return ok
	})
	object, _ := w.api.object(podsCollection, name)
	object["status"] = map[string]any{"phase": "Pending", "conditions": []any{map[string]any{
		"type": "PodScheduled", "status": "False", "reason": "Unschedulable", "message": message,
	}}}
	w.api.mu.Lock()
	w.api.store(podsCollection, name, object, "MODIFIED")
	w.api.mu.Unlock()
}

// A camera pod that cannot be scheduled, such as one whose outbox is
// bound on another node, keeps StartDevices waiting, and the step's
// summary reports the scheduler's message.
func TestAPodThatCannotBeScheduledShowsTheSchedulersMessage(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.putClaim("frames")
		w.api.holdPending("east-main-camera")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "outbox": map[string]any{"claimName": "frames"}})
		message := "0/3 nodes are available: 3 node(s) had volume node affinity conflict."
		w.holdUnschedulable("east-main-camera", message)

		want := "Waiting for pod east-main-camera of Camera east-main (Pending: " + message + ")"
		w.until(5*time.Minute, "StartDevices does not give the scheduler's message", func() bool {
			r, _ := w.reservation("east-tonight")
			return stepOf(r, observatory.StepStartDevices).Summary == want
		})
	})
}
