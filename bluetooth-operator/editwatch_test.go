package main

// These tests check which events of the Adapter and Peripheral watches
// wake the loop, and that the Peripheral watch follows the radio that
// the pass reports.

import (
	"fmt"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
)

func peripheralAt(generation int64, deleting bool) Peripheral {
	peripheral := Peripheral{
		APIVersion: pairingAPI,
		Kind:       peripheralKind,
		Metadata:   ObjectMeta{Name: "a0-ab-51-33-b7-12", Generation: generation},
	}
	if deleting {
		peripheral.Metadata.DeletionTimestamp = timestamp(testNow)
	}
	return peripheral
}

func adapterOn(name, node string, generation int64) Adapter {
	return Adapter{
		APIVersion: pairingAPI,
		Kind:       adapterKind,
		Metadata:   ObjectMeta{Name: name, Generation: generation},
		Status:     AdapterStatus{Node: node},
	}
}

func updated(before, after any) func(cache.ResourceEventHandler) {
	return func(h cache.ResourceEventHandler) { h.OnUpdate(before, after) }
}

func added(item any) func(cache.ResourceEventHandler) {
	return func(h cache.ResourceEventHandler) { h.OnAdd(item, false) }
}

func removed(item any) func(cache.ResourceEventHandler) {
	return func(h cache.ResourceEventHandler) { h.OnDelete(item) }
}

// Each update starts from a cached copy at generation 1.
func TestAChangeWakesTheLoopOnlyForAnEdit(t *testing.T) {
	cached := asObject(t, peripheralAt(1, false))
	recreated := peripheralAt(1, false)
	recreated.Metadata.UID = "5f0c2a1e-8d3b-4c6f-9a7e-2b1d4e6f8a90"
	cases := []struct {
		name    string
		deliver func(cache.ResourceEventHandler)
		want    bool
	}{
		{name: "a status write", deliver: updated(cached, asObject(t, peripheralAt(1, false))), want: false},
		{name: "a spec edit", deliver: updated(cached, asObject(t, peripheralAt(2, false))), want: true},
		{name: "a deletion request", deliver: updated(cached, asObject(t, peripheralAt(1, true))), want: true},
		{name: "a removed object", deliver: removed(asObject(t, peripheralAt(1, true))), want: true},
		{
			name:    "an object removed while the watch was down",
			deliver: removed(cache.DeletedFinalStateUnknown{Key: "a0-ab-51-33-b7-12", Obj: cached}),
			want:    true,
		},
		{name: "a new object", deliver: added(asObject(t, peripheralAt(1, false))), want: true},
		{name: "an object deleted and created again with the same name", deliver: updated(cached, asObject(t, recreated)), want: true},
		{
			name:    "a removal whose tombstone has no copy",
			deliver: removed(cache.DeletedFinalStateUnknown{Key: "a0-ab-51-33-b7-12"}),
			want:    true,
		},
		{name: "a cached copy that does not convert", deliver: updated("not an object", cached), want: true},
		{name: "an object that does not convert", deliver: added("not an object"), want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wakes := 0
			handler := editHandler[Peripheral]{what: "the Peripherals", wake: func() { wakes++ }, mark: peripheralMark}
			c.deliver(handler.handler())
			if got := wakes == 1; got != c.want {
				t.Fatalf("the change woke the loop: %t, want %t", got, c.want)
			}
		})
	}
}

func TestAnAdapterEditWakesTheLoopOnlyOnItsNode(t *testing.T) {
	cases := []struct {
		name    string
		adapter Adapter
		want    bool
	}{
		{name: "the radio this pod uses", adapter: adapterOn(testAdapterName, "node-2", 2), want: true},
		{name: "another radio on this node", adapter: adapterOn("00-1a-7d-da-71-13", "node-1", 2), want: true},
		{name: "a radio that no node has reported yet", adapter: adapterOn("00-1a-7d-da-71-13", "", 2), want: true},
		{name: "a radio on another node", adapter: adapterOn("00-1a-7d-da-71-13", "node-2", 2), want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			watch := &editWatch{nodeName: "node-1", adapterKeyChanged: make(chan struct{}, 1)}
			watch.follow(testAdapterName)
			wakes := 0
			handler := editHandler[Adapter]{what: "the Adapters", wake: func() { wakes++ }, mark: watch.adapterMark}
			before := c.adapter
			before.Metadata.Generation = 1
			handler.handler().OnUpdate(asObject(t, before), asObject(t, c.adapter))
			if got := wakes == 1; got != c.want {
				t.Fatalf("the edit woke the loop: %t, want %t", got, c.want)
			}
		})
	}
}

// editServers serves the Adapter and Peripheral collections, each from
// its own scripted server.
func editServers(t *testing.T, adapters, peripherals *watchServer) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(adaptersPath(), adapters.handler(t))
	mux.Handle(peripheralsPath(), peripherals.handler(t))
	return mux
}

// runEditWatch runs the edit watcher until the test ends.
func runEditWatch(t *testing.T, handler http.Handler) *editWatch {
	t.Helper()
	watch := watchEdits(t.Context(), testWatcher(t, handler), "node-1")
	t.Cleanup(func() {
		for range watch.wakes() {
		}
	})
	return watch
}

func peripheralEvent(t *testing.T, version string, peripheral Peripheral) string {
	t.Helper()
	peripheral.Metadata.ResourceVersion = version
	return fmt.Sprintf(`{"type":"MODIFIED","object":%s}`, encode(t, peripheral))
}

// The scripted server sends each event only after the test has read
// the wakes that came before it.
func TestAPeripheralEditWakesTheLoopAndAStatusWriteDoesNot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		adapters := newWatchServer(adaptersPath(), "AdapterList", []string{"[]"})
		peripherals := newWatchServer(peripheralsPath(), "PeripheralList", []string{"[" + encode(t, peripheralAt(1, false)) + "]"}, []string{
			pause, peripheralEvent(t, "2", peripheralAt(1, false)),
			pause, peripheralEvent(t, "3", peripheralAt(2, false)),
			holdOpen,
		})
		watch := runEditWatch(t, editServers(t, adapters, peripherals))
		watch.follow(testAdapterName)
		synctest.Wait()

		// Each watch wakes the loop when it starts.
		if adapters.opened() != 1 || peripherals.opened() != 1 || !woke(watch.wakes()) {
			t.Fatal("the watches did not start and wake the loop")
		}
		peripherals.release()
		synctest.Wait()
		if woke(watch.wakes()) {
			t.Fatal("the status write woke the loop")
		}
		peripherals.release()
		synctest.Wait()
		if !woke(watch.wakes()) {
			t.Fatal("the spec edit did not wake the loop")
		}
	})
}

// Each watch wakes the loop when its first list completes, even when
// the list is empty.
func TestAWatchWakesTheLoopWhenItsFirstReadIsDone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		adapters := newWatchServer(adaptersPath(), "AdapterList", []string{"[]"})
		peripherals := newWatchServer(peripheralsPath(), "PeripheralList", []string{"[]"})
		watch := runEditWatch(t, editServers(t, adapters, peripherals))
		synctest.Wait()
		if adapters.opened() != 1 || !woke(watch.wakes()) {
			t.Fatal("the Adapter watch did not wake the loop when its first read was done")
		}

		watch.follow(testAdapterName)
		synctest.Wait()
		if peripherals.opened() != 1 || !woke(watch.wakes()) {
			t.Fatal("the Peripheral watch did not wake the loop when its first read was done")
		}
	})
}

// When a watch closes immediately, the reflector lists the collection
// again after its backoff, and the informer reports what differs
// between the two lists.
func TestAChangeWhileTheWatchWasDownWakesTheLoopOnlyForAnEdit(t *testing.T) {
	cases := []struct {
		name  string
		after Peripheral
		want  bool
	}{
		{name: "a status write", after: peripheralAt(1, false), want: false},
		{name: "a spec edit", after: peripheralAt(2, false), want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				adapters := newWatchServer(adaptersPath(), "AdapterList", []string{"[]"})
				reads := []string{"[" + encode(t, peripheralAt(1, false)) + "]", "[" + encode(t, c.after) + "]"}
				peripherals := newWatchServer(peripheralsPath(), "PeripheralList", reads, []string{}, []string{holdOpen})
				watch := runEditWatch(t, editServers(t, adapters, peripherals))
				watch.follow(testAdapterName)
				synctest.Wait()
				if peripherals.opened() != 1 || !woke(watch.wakes()) {
					t.Fatal("the first Peripheral watch did not start and wake the loop")
				}

				// Longer than the reflector's backoff.
				time.Sleep(time.Minute)
				synctest.Wait()
				if peripherals.opened() != 2 {
					t.Fatalf("the watcher opened %d Peripheral watches, want 2", peripherals.opened())
				}
				if got := woke(watch.wakes()); got != c.want {
					t.Fatalf("the change woke the loop: %t, want %t", got, c.want)
				}
			})
		})
	}
}

// A second report of the same radio keeps the Peripheral watch open,
// and a new radio replaces it with a watch on the new radio's
// Peripherals.
func TestThePeripheralWatchFollowsTheRadio(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		adapters := newWatchServer(adaptersPath(), "AdapterList", []string{"[]"})
		peripherals := newWatchServer(peripheralsPath(), "PeripheralList", []string{"[]"})
		watch := runEditWatch(t, editServers(t, adapters, peripherals))
		const other = "00-1a-7d-da-71-13"

		watch.follow(testAdapterName)
		synctest.Wait()
		watch.follow(testAdapterName)
		watch.follow(other)
		synctest.Wait()

		want := fmt.Sprint([]string{adapterSelector(testAdapterName), adapterSelector(other)})
		if open, selectors := peripherals.openWatches(); open != 1 || fmt.Sprint(selectors) != want {
			t.Fatalf("the server has %d open Peripheral watches selecting %v, want 1 watch selecting %s", open, selectors, want)
		}
	})
}

func TestAPassReportsItsRadioToTheEditWatcher(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))
	var followed []string
	inventory.follow = func(key string) { followed = append(followed, key) }

	inventory.reconcile()

	if fmt.Sprint(followed) != "["+testAdapterName+"]" {
		t.Fatalf("the pass reported %v, want [%s]", followed, testAdapterName)
	}
}

// The Peripheral store returns its view only for the radio that the
// watch follows.
func TestTheEditWatchesFillTheStoresThePassReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		adapters := newWatchServer(adaptersPath(), "AdapterList", []string{"[" + encode(t, adapterOn(testAdapterName, "node-1", 1)) + "]"})
		peripherals := newWatchServer(peripheralsPath(), "PeripheralList", []string{"[" + encode(t, peripheralAt(1, false)) + "]"})
		watch := runEditWatch(t, editServers(t, adapters, peripherals))
		watch.follow(testAdapterName)
		stores := watch.cache(informer.View{})
		synctest.Wait()

		if !stores.adapters.View.Ready() || !stores.peripherals.of(testAdapterName).Ready() {
			t.Fatal("the Adapter and Peripheral stores are not ready after their first reads, want both ready")
		}
		if listed := informer.CachedList[Adapter](stores.adapters.View); len(listed) != 1 || listed[0].Metadata.Name != testAdapterName {
			t.Errorf("the Adapter store contains %+v, want one Adapter named %s", listed, testAdapterName)
		}
		if listed := informer.CachedList[Peripheral](stores.peripherals.of(testAdapterName)); len(listed) != 1 {
			t.Errorf("the Peripheral store contains %+v, want one Peripheral", listed)
		}
		if stores.peripherals.of("00-1a-7d-da-71-13").Ready() {
			t.Error("the Peripheral store is ready for a radio that the watch does not follow, want not ready")
		}
	})
}
