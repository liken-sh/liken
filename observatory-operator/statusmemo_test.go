package main

// The status writer's memo: a window in which nothing changed reads
// every status but compares little, and a status that another writer
// changes is written again.

import (
	"testing"
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
		tree, seen := w.o.snapshot(), &statusMemo{}
		w.o.writeAll(t.Context(), tree, seen)
		synctest.Wait()
		before := w.api.statusWrites(kindCollection(observatory.CameraKind), "east-main")
		allocs := testing.AllocsPerRun(10, func() { w.o.writeAll(t.Context(), tree, seen) })
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
		w.api.overwriteStatus(collection, "east-focuser", map[string]any{"phase": "Error"})
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		focuser, _ := decode[observatory.Focuser](t, w.api, collection, "east-focuser")
		if focuser.Status.Phase != observatory.DeviceConnected {
			t.Errorf("phase = %q, want %q", focuser.Status.Phase, observatory.DeviceConnected)
		}
	})
}
