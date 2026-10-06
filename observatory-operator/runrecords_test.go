package main

// The record of a resource's runs belongs to one object, not to one
// name: an object deleted and created again with the same name starts
// with no runs.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// recreate deletes one of the example's objects, waits until the
// operator's store holds no object of that name, and creates it again
// from the same spec, as `kubectl delete` and `kubectl apply` do.
func (w *world) recreate(kind observatory.Kind, name string) {
	w.t.Helper()
	collection := kindCollection(kind)
	object, ok := w.api.object(collection, name)
	if !ok {
		w.t.Fatalf("no %s %s", kind.Name, name)
	}
	w.api.deleteNamed(collection, name)
	w.until(time.Minute, kind.Name+" "+name+" is not gone", func() bool {
		_, held := w.o.snapshot().resource(kind, name)
		return !held
	})
	w.api.put(collection, map[string]any{
		"apiVersion": object["apiVersion"], "kind": object["kind"],
		"metadata": map[string]any{"name": name},
		"spec":     object["spec"],
	})
}

func TestARecreatedDomeStartsWithNoRunsOfTheOldOne(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.until(time.Minute, "the dome's status does not record its activation", func() bool {
			dome, _ := decode[observatory.Dome](t, w.api, kindCollection(observatory.DomeKind), "lab")
			return len(dome.Status.Procedures) == 1
		})

		recreated := time.Now()
		w.recreate(observatory.DomeKind, "lab")
		w.until(5*time.Minute, "the new dome does not connect", func() bool {
			dome, _ := decode[observatory.Dome](t, w.api, kindCollection(observatory.DomeKind), "lab")
			return dome.Status.Phase == observatory.DeviceConnected
		})
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		dome, _ := decode[observatory.Dome](t, w.api, kindCollection(observatory.DomeKind), "lab")
		for _, run := range dome.Status.Procedures {
			if run.StartTime == nil || run.StartTime.Before(recreated) {
				t.Errorf("the new dome holds a run of the old one: %s", mustJSON(run))
			}
		}
	})
}
