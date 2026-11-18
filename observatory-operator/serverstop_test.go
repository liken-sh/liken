package main

import (
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// podDeletes records the name of each pod that the operator deletes,
// in the order the fake API server receives the requests.
type podDeletes struct {
	mu    sync.Mutex
	names []string
}

func (d *podDeletes) record(r *http.Request) {
	prefix := podsCollection + "/"
	if r.Method != http.MethodDelete || !strings.HasPrefix(r.URL.Path, prefix) {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.names = append(d.names, strings.TrimPrefix(r.URL.Path, prefix))
}

func (d *podDeletes) deleted() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.names)
}

// When a release stops an INDI server, the operator deletes the pods
// of the server's devices before the server's pod, so a device's
// container does not restart while its server stops. The test keeps
// the dome's pod terminating, and the reservation must not be Released
// until the pod is gone.
func TestAServerStopsAfterItsDevicePods(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		deletes := &podDeletes{}
		w.api.onEachRequest(deletes.record)
		w.holdPod("lab-dome")
		at := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": at})
		w.until(15*time.Minute, "the dome's pod is not deleted", func() bool {
			object, _ := w.api.object(podsCollection, "lab-dome")
			return object["metadata"].(map[string]any)["deletionTimestamp"] != nil
		})
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if r, _ := w.reservation("east-tonight"); r.Status.Phase == observatory.ReservationReleased {
			t.Error("the reservation is Released, want it to stay unreleased while the dome's pod is terminating")
		}
		w.releasePod("lab-dome")
		w.phase("east-tonight", observatory.ReservationReleased, 15*time.Minute)

		devicesFirst(t, deletes.deleted(), []podOrder{
			{"east-switch", "east-telescope"},
			{"lab-dome", "lab-observatory"},
			{"lab-weatherstation", "lab-observatory"},
		})
	})
}

// podOrder is the pod of a device and the pod of the INDI server that
// the device connects to.
type podOrder struct{ device, server string }

// devicesFirst checks that each device's pod is in deleted before the
// pod of its server.
func devicesFirst(t *testing.T, deleted []string, orders []podOrder) {
	t.Helper()
	for _, order := range orders {
		device, server := slices.Index(deleted, order.device), slices.Index(deleted, order.server)
		if device < 0 || server < 0 || device > server {
			t.Errorf("the pod %s was deleted at position %d and its server's pod %s at position %d, want the device's pod first, in %v",
				order.device, device, order.server, server, deleted)
		}
	}
}

// A Ready reservation is deleted while the operator is down, so no
// deactivation runs. When the operator starts again, it deletes the
// pods that no reservation uses, each server's device pods before the
// server's pod.
func TestTheSweepStopsAServerAfterItsDevicePods(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.halt()
		object, _ := w.api.object(kindCollection(observatory.ReservationKind), "east-tonight")
		w.api.mu.Lock()
		w.api.store(kindCollection(observatory.ReservationKind), "east-tonight", object, "DELETED")
		w.api.mu.Unlock()
		deletes := &podDeletes{}
		w.api.onEachRequest(deletes.record)
		w.start()
		w.until(time.Minute, "the sweep leaves pods", func() bool { return len(w.api.names(podsCollection)) == 0 })

		devicesFirst(t, deletes.deleted(), []podOrder{
			{"east-switch", "east-telescope"},
			{"east-mount", "east-telescope"},
			{"east-main-camera", "east-telescope"},
			{"east-guider", "east-telescope"},
			{"lab-dome", "lab-observatory"},
			{"lab-weatherstation", "lab-observatory"},
		})
	})
}
