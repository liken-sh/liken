package main

// The operator's finalizer on a device only keeps the object while the
// device is being deleted. When a person removes the finalizer by hand
// from a device whose pod runs, the release of the reservation still
// parks and disconnects the device.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// dropFinalizer removes every finalizer from a device, as a person
// does with kubectl patch.
func (w *world) dropFinalizer(kind observatory.Kind, name string) {
	object, _ := w.api.object(kindCollection(kind), name)
	delete(object["metadata"].(map[string]any), "finalizers")
	w.api.mu.Lock()
	w.api.store(kindCollection(kind), name, object, "MODIFIED")
	w.api.mu.Unlock()
}

func TestAReleaseParksADustCapWhoseFinalizerAPersonRemoved(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.dropFinalizer(observatory.DustCapKind, "east")
		before := len(w.indi.changes())

		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.gone(observatory.ReservationKind, "east-tonight", 10*time.Minute)

		sent := w.indi.changes()[before:]
		for _, want := range []string{parkCap, "east-telescope Dust Cover Simulator.CONNECTION CONNECT=Off DISCONNECT=On"} {
			if !slices.Contains(sent, want) {
				t.Errorf("the changes sent after the release are %q, want %q among them", sent, want)
			}
		}
	})
}
