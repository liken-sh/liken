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

// The watch's copy of a device holds no status.properties, because no
// pass reads the list and it is most of the object. The API server
// still holds the list that the status writer wrote.
func TestTheCachedDeviceHoldsNoPropertyList(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		cached, _, _ := w.operator().stores.kinds[observatory.CameraKind].View().Store.GetByKey(testNamespace + "/east-main")
		if _, found, _ := unstructured.NestedSlice(cached.(*unstructured.Unstructured).Object, "status", "properties"); found {
			t.Error("the cached Camera east-main holds status.properties")
		}
		camera, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "east-main")
		if len(camera.Status.Properties) == 0 {
			t.Error("the API server's Camera east-main holds no status.properties")
		}
	})
}
