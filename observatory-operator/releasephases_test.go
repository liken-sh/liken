package main

// When a release stops a device, the device reports Disconnecting from
// its disconnect until its pod is gone, and then Idle. It never reports
// Starting, because nothing is starting it.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// holdPod adds a finalizer to a pod, so that a delete leaves it
// terminating, as a real pod is while the kubelet stops its containers.
func (w *world) holdPod(name string) {
	object, _ := w.api.object(podsCollection, name)
	object["metadata"].(map[string]any)["finalizers"] = []any{"test.liken.sh/hold"}
	w.api.mu.Lock()
	w.api.store(podsCollection, name, object, "MODIFIED")
	w.api.mu.Unlock()
}

// releasePod removes a pod that holdPod kept terminating.
func (w *world) releasePod(name string) {
	object, _ := w.api.object(podsCollection, name)
	w.api.mu.Lock()
	w.api.store(podsCollection, name, object, "DELETED")
	w.api.mu.Unlock()
}

// The shim applies each driver change 10 seconds late, so the camera's
// driver stops 10 seconds after the operator asks, on a server that
// keeps running for the Switch. The dome's pod stays terminating for
// 10 seconds after StopSite deletes it.
func TestAReleasedDeviceIsNeverStarting(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.slowShim(10 * time.Second)
		w.holdPod("lab-dome")
		w.api.mu.Lock()
		since := w.api.version
		w.api.mu.Unlock()
		at := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": at})
		w.until(15*time.Minute, "the dome's pod does not stop", func() bool {
			object, _ := w.api.object(podsCollection, "lab-dome")
			return object["metadata"].(map[string]any)["deletionTimestamp"] != nil
		})
		time.Sleep(10 * time.Second)
		w.releasePod("lab-dome")
		w.phase("east-tonight", observatory.ReservationReleased, 15*time.Minute)
		w.settle()
		for _, d := range []struct {
			kind observatory.Kind
			name string
		}{{observatory.CameraKind, "east-main"}, {observatory.DomeKind, "lab"}} {
			phases := devicePhases(w.api, d.kind, d.name, since)
			if slices.Contains(phases, string(observatory.DeviceStarting)) || !slices.Contains(phases, string(observatory.DeviceDisconnecting)) ||
				phases[len(phases)-1] != string(observatory.DeviceIdle) {
				t.Errorf("the phases of %s %s were %q, want Disconnecting, never Starting, and Idle at the end", d.kind.Name, d.name, phases)
			}
		}
	})
}

// After a release, a device reports no endpoint, INDI device, pod, or
// node, because a client that dialed the endpoint of an Idle device
// would reach a server that no longer exists. The filter wheel is
// served by the camera's pod, so it loses them when that pod is
// deleted.
func TestAReleasedDeviceHasNoServer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		at := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": at})
		w.phase("east-tonight", observatory.ReservationReleased, 15*time.Minute)
		w.settle()
		camera, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "east-main")
		if s := camera.Status; s.Phase != observatory.DeviceIdle || s.Endpoint != nil || s.IndiDevice != "" || s.Pod != "" || s.Node != "" {
			t.Errorf("the released camera has phase %s, INDI device %q, endpoint %+v, pod %q, node %q, want Idle with none of the others", s.Phase, s.IndiDevice, s.Endpoint, s.Pod, s.Node)
		}
		wheel, _ := decode[observatory.FilterWheel](t, w.api, kindCollection(observatory.FilterWheelKind), "east")
		if s := wheel.Status; s.Phase != observatory.DeviceIdle || s.Endpoint != nil || s.IndiDevice != "" || s.Slot != nil {
			t.Errorf("the released filter wheel has phase %s, INDI device %q, endpoint %+v, slot %+v, want Idle with none of the others", s.Phase, s.IndiDevice, s.Endpoint, s.Slot)
		}
	})
}

// A device reports no endpoint once its pod is terminating, because its
// driver stopped before the operator deleted the pod. The focuser's
// finalizer keeps it in the API while holdPod keeps its pod
// terminating.
func TestADeviceWhosePodStopsHasNoServer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.holdPod("east-focuser")
		w.api.deleteNamed(kindCollection(observatory.FocuserKind), "east")
		w.until(5*time.Minute, "the focuser's pod does not stop", func() bool {
			object, _ := w.api.object(podsCollection, "east-focuser")
			return object["metadata"].(map[string]any)["deletionTimestamp"] != nil
		})
		w.settle()

		focuser, _ := decode[observatory.Focuser](t, w.api, kindCollection(observatory.FocuserKind), "east")
		if s := focuser.Status; s.Endpoint != nil {
			t.Errorf("the focuser in phase %s has endpoint %+v, want none", s.Phase, s.Endpoint)
		}
	})
}

// A person deletes the reservation and its OpticalTrain in one
// command. The train's devices remain, and their pods are the only
// record of which server they ran on, so the Disconnect step
// disconnects them there. Each device reports Disconnecting until its
// pod is gone and Idle after that, with no Starting phase and no
// Starting Event.
func TestADeviceWhoseTrainIsDeletedIsNeverStarting(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.slowShim(10 * time.Second)
		devices := []named{{observatory.CameraKind, "east-main"}, {observatory.FocuserKind, "east"}, {observatory.FlatPanelKind, "east"}}
		before := map[named]int{}
		for _, d := range devices {
			before[d] = len(typedEvents(w.api, d.kind, d.name))
		}
		w.api.mu.Lock()
		since := w.api.version
		w.api.mu.Unlock()
		w.deleteDuringAbort(named{observatory.OpticalTrainKind, "east-imaging"})
		w.gone(observatory.ReservationKind, "east-tonight", 15*time.Minute)
		w.settle()
		for _, d := range devices {
			phases := devicePhases(w.api, d.kind, d.name, since)
			if slices.Contains(phases, string(observatory.DeviceStarting)) || !slices.Contains(phases, string(observatory.DeviceDisconnecting)) ||
				phases[len(phases)-1] != string(observatory.DeviceIdle) {
				t.Errorf("the phases of %s %s were %q, want Disconnecting, never Starting, and Idle at the end", d.kind.Name, d.name, phases)
			}
			release := typedEvents(w.api, d.kind, d.name)[before[d]:]
			if slices.ContainsFunc(release, func(e string) bool { return strings.Contains(e, " Starting: ") }) {
				t.Errorf("the Events of %s %s during the release were %q, want no Starting", d.kind.Name, d.name, release)
			}
		}
	})
}
