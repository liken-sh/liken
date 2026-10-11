package main

// The status writer's memo: a window in which nothing changed reads
// every status but compares little, and a status that another writer
// changes is written again.

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// A window with no change writes nothing, and its allocations stay
// far below a JSON round trip of each device's property list, which
// cost about 4,300 allocations for each of the example's 17 devices.
//
// AllocsPerRun counts the whole process, so the test does not run in
// parallel with others.
func TestAWindowWithNoChangeComparesLittle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		o := w.operator()
		tree, seen := o.snapshot(), &statusMemo{}
		o.writeAll(t.Context(), tree, seen)
		synctest.Wait()
		before := w.api.statusWrites(kindCollection(observatory.CameraKind), "east-main")
		allocs := testing.AllocsPerRun(10, func() { o.writeAll(t.Context(), tree, seen) })
		if limit := float64(400 * len(tree.devices)); allocs > limit {
			t.Errorf("%v allocations in a window with no change, want at most %v", allocs, limit)
		}
		if writes := w.api.statusWrites(kindCollection(observatory.CameraKind), "east-main") - before; writes != 0 {
			t.Errorf("%d status writes in windows with no change", writes)
		}
	})
}

// overwriteStatus sets an object's status, as another writer does.
func (a *fakeAPI) overwriteStatus(collection, name string, status map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	next := clone(a.objects[collection][name])
	next["status"] = status
	a.store(collection, name, next, "MODIFIED")
}

func TestAStatusThatAnotherWriterChangesIsWrittenAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		collection := kindCollection(observatory.FocuserKind)
		w.api.overwriteStatus(collection, "east", map[string]any{"phase": "Error"})
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		focuser, _ := decode[observatory.Focuser](t, w.api, collection, "east")
		if focuser.Status.Phase != observatory.DeviceConnected {
			t.Errorf("phase = %q, want %q", focuser.Status.Phase, observatory.DeviceConnected)
		}
	})
}

// The watch's copy of a device holds none of the fields that the
// status writer composes from INDI, because no pass reads them and they
// are most of the object. The API server still holds what the status
// writer wrote.
func TestTheCachedDeviceHoldsNoComposedFields(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		cached, _, _ := w.operator().stores.kinds[observatory.CameraKind].View().Store.GetByKey(testNamespace + "/east-main")
		status, _, _ := unstructured.NestedMap(cached.(*unstructured.Unstructured).Object, "status")
		stored, _ := w.api.object(kindCollection(observatory.CameraKind), "east-main")
		for _, field := range []string{"properties", "limits", "temperature", "gain"} {
			if _, found := status[field]; found {
				t.Errorf("the cached Camera east-main holds status.%s", field)
			}
			if _, found := stored["status"].(map[string]any)[field]; !found {
				t.Errorf("the API server's Camera east-main holds no status.%s", field)
			}
		}
		if status["phase"] != string(observatory.DeviceConnected) {
			t.Errorf("the cached Camera east-main's phase = %v, want %s", status["phase"], observatory.DeviceConnected)
		}
	})
}
