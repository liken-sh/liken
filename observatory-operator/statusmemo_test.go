package main

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// AllocsPerRun counts the allocations of the whole process, so this
// test does not run in parallel with others.
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
			t.Errorf("a window with no change allocated %v times, want at most %v", allocs, limit)
		}
		if writes := w.api.statusWrites(kindCollection(observatory.CameraKind), "east-main") - before; writes != 0 {
			t.Errorf("windows with no change wrote status %d times, want 0", writes)
		}
	})
}

// overwriteStatus replaces an object's status, as another client would.
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
			t.Errorf("after the window, the Focuser east phase is %q, want %q", focuser.Status.Phase, observatory.DeviceConnected)
		}
	})
}

// The watch's cache omits the status fields that the writer composes
// from INDI, because no pass reads them. The API server still stores
// them.
func TestTheCachedDeviceHoldsNoComposedFields(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		cached, _, _ := w.operator().stores.kinds[observatory.CameraKind].View().Store.GetByKey(testNamespace + "/east-main")
		status, _, _ := unstructured.NestedMap(cached.(*unstructured.Unstructured).Object, "status")
		stored, _ := w.api.object(kindCollection(observatory.CameraKind), "east-main")
		for _, field := range []string{"properties", "limits", "temperature", "gain"} {
			if _, found := status[field]; found {
				t.Errorf("the cached Camera east-main has status.%s, want it omitted", field)
			}
			if _, found := stored["status"].(map[string]any)[field]; !found {
				t.Errorf("the API server's Camera east-main has no status.%s, want it stored", field)
			}
		}
		if status["phase"] != string(observatory.DeviceConnected) {
			t.Errorf("the cached Camera east-main phase is %v, want %s", status["phase"], observatory.DeviceConnected)
		}
	})
}
