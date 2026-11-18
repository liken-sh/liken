package main

// status.procedures belongs to one object, not to one name. A dome that
// is deleted and created again with the same name starts with no runs.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// recreate deletes an object, waits until the operator's snapshot no
// longer has it, and creates it again from the same spec, as
// `kubectl delete` and `kubectl apply` do.
func (w *world) recreate(kind observatory.Kind, name string) {
	w.t.Helper()
	collection := kindCollection(kind)
	object, ok := w.api.object(collection, name)
	if !ok {
		w.t.Fatalf("the %s %s does not exist", kind.Name, name)
	}
	w.api.deleteNamed(collection, name)
	w.until(time.Minute, kind.Name+" "+name+" is not gone", func() bool {
		_, held := w.operator().snapshot().resource(kind, name)
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
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.gone(observatory.ReservationKind, "east-tonight", 10*time.Minute)
		w.reserve("east-later", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-later", observatory.ReservationReady, 10*time.Minute)
		w.settle()
		dome, _ := decode[observatory.Dome](t, w.api, kindCollection(observatory.DomeKind), "lab")
		if dome.Status.Phase != observatory.DeviceConnected || len(dome.Status.Procedures) == 0 {
			t.Errorf("the recreated dome is %s with runs %s, want Connected with its own activation", dome.Status.Phase, mustJSON(dome.Status.Procedures))
		}
		for _, run := range dome.Status.Procedures {
			if run.StartTime == nil || run.StartTime.Before(recreated) {
				t.Errorf("the recreated dome holds a run of the old dome: %s", mustJSON(run))
			}
		}
	})
}
