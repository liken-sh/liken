package main

// A device that a release stops reports Disconnecting from its
// disconnect until its pod is gone, and then Idle. Starting would say
// that something brings it up.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// holdPod gives a pod a finalizer, so a delete leaves it terminating,
// as a kubelet does while the containers stop.
func (w *world) holdPod(name string) {
	object, _ := w.api.object(podsCollection, name)
	object["metadata"].(map[string]any)["finalizers"] = []any{"test.liken.sh/hold"}
	w.api.mu.Lock()
	w.api.store(podsCollection, name, object, "MODIFIED")
	w.api.mu.Unlock()
}

// releasePod removes a held pod that a delete left terminating.
func (w *world) releasePod(name string) {
	object, _ := w.api.object(podsCollection, name)
	w.api.mu.Lock()
	w.api.store(podsCollection, name, object, "DELETED")
	w.api.mu.Unlock()
}

// The camera's driver stops on the server that keeps running for the
// Switch, 10 seconds after the operator asks, and the dome's server
// stops at StopSite, its pod terminating for 10 seconds while the dome's
// pod stays.
func TestAReleasedDeviceIsNeverStarting(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.slowShim(10 * time.Second)
		w.holdPod("lab-observatory")
		w.api.mu.Lock()
		since := w.api.version
		w.api.mu.Unlock()
		at := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": at})
		w.until(15*time.Minute, "the observatory's server does not stop", func() bool {
			object, _ := w.api.object(podsCollection, "lab-observatory")
			return object["metadata"].(map[string]any)["deletionTimestamp"] != nil
		})
		time.Sleep(10 * time.Second)
		w.releasePod("lab-observatory")
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
