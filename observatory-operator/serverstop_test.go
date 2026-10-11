package main

// The order of the deletes when a release stops an INDI server: the
// pods of its devices before the server's pod, so that no device's
// container runs again while its server stops.

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
// in the order the API server receives the deletes.
type podDeletes struct {
	mu    sync.Mutex
	names []string
}

func (d *podDeletes) hear(r *http.Request) {
	prefix := podsCollection + "/"
	if r.Method != http.MethodDelete || !strings.HasPrefix(r.URL.Path, prefix) {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.names = append(d.names, strings.TrimPrefix(r.URL.Path, prefix))
}

func (d *podDeletes) sent() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.names)
}

// The dome's pod stays terminating for 10 seconds, and the reservation
// is released only after it is gone. The deletes of the device pods
// reach the API server before the delete of their server's pod.
func TestAServerStopsAfterItsDevicePods(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		deletes := &podDeletes{}
		w.api.hear(deletes.hear)
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
			t.Error("the reservation is Released while the dome's pod is terminating")
		}
		w.releasePod("lab-dome")
		w.phase("east-tonight", observatory.ReservationReleased, 15*time.Minute)

		sent := deletes.sent()
		for _, order := range []struct{ device, server string }{
			{"east-switch", "east-telescope"},
			{"lab-dome", "lab-observatory"},
			{"lab-weatherstation", "lab-observatory"},
		} {
			device, server := slices.Index(sent, order.device), slices.Index(sent, order.server)
			if device < 0 || server < 0 || device > server {
				t.Errorf("deleted pod %s at %d and its server's pod %s at %d, want the device's pod first, in %v",
					order.device, device, order.server, server, sent)
			}
		}
	})
}
