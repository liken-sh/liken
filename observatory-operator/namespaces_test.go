package main

// Each namespace's observatory runs apart from the others, with the
// same names.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

const secondNamespace = "west-wing"

// putExample creates the example's resources in a namespace.
func putExample(w *world, namespace string) {
	for _, object := range example(w.t) {
		w.api.put(kindNamed(object["kind"].(string)).Path(namespace), object)
	}
}

// A reservation in one namespace runs that namespace's telescope, and
// the telescope of the same name in another namespace stays idle.
func TestTwoNamespacesRunTheirObservatoriesApart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		putExample(w, secondNamespace)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.until(time.Minute, "the telescope of "+secondNamespace+" has no status", func() bool {
			return w.api.statusWrites(observatory.TelescopeKind.Path(secondNamespace), "east") > 0
		})

		held, _ := decode[observatory.Telescope](t, w.api, kindCollection(observatory.TelescopeKind), "east")
		idle, _ := decode[observatory.Telescope](t, w.api, observatory.TelescopeKind.Path(secondNamespace), "east")
		if held.Status.Reservation == nil || idle.Status.Reservation != nil {
			t.Errorf("reservations = %+v and %+v, want only %s's telescope held", held.Status.Reservation, idle.Status.Reservation, testNamespace)
		}
		if pods := w.api.names("/api/v1/namespaces/" + secondNamespace + "/pods"); len(pods) != 0 {
			t.Errorf("pods in %s = %v, want none", secondNamespace, pods)
		}
	})
}

// A namespace gets its operator with its first resource, and loses it
// when its last resource is gone.
func TestANamespaceRunsWhileItHoldsResources(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		sites := observatory.ObservatoryKind.Path(secondNamespace)
		w.api.put(sites, map[string]any{
			"apiVersion": observatory.Group + "/" + observatory.Version, "kind": observatory.ObservatoryKind.Name,
			"metadata": map[string]any{"name": "lab"}, "spec": map[string]any{},
		})
		w.until(time.Minute, secondNamespace+" has no operator", func() bool {
			return w.n.operatorOf(secondNamespace) != nil
		})

		w.api.deleteNamed(sites, "lab")

		w.until(time.Minute, secondNamespace+" still has an operator", func() bool {
			return w.n.operatorOf(secondNamespace) == nil
		})
		if w.n.operatorOf(testNamespace) == nil {
			t.Errorf("%s has no operator", testNamespace)
		}
	})
}
