package main

// These tests cover what wakes the loop for an Adapter or a Peripheral:
// a spec edit, a deletion request, a new object, and a removed object
// wake it. A status write, and an Adapter that another node holds, do
// not. The Peripheral watch follows the radio the pass reports.

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"k8s.io/client-go/tools/cache"
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

// The informer calls a handler in one of three ways. Each builds the
// call for one case of a table.
func updated(before, after any) func(cache.ResourceEventHandler) {
	return func(h cache.ResourceEventHandler) { h.OnUpdate(before, after) }
}

func added(item any) func(cache.ResourceEventHandler) {
	return func(h cache.ResourceEventHandler) { h.OnAdd(item, false) }
}

func removed(item any) func(cache.ResourceEventHandler) {
	return func(h cache.ResourceEventHandler) { h.OnDelete(item) }
}

// Each update starts from a held copy at generation 1.
func TestAChangeWakesTheLoopOnlyForAnEdit(t *testing.T) {
	held := asObject(t, peripheralAt(1, false))
	recreated := peripheralAt(1, false)
	recreated.Metadata.UID = "5f0c2a1e-8d3b-4c6f-9a7e-2b1d4e6f8a90"
	cases := []struct {
		name    string
		deliver func(cache.ResourceEventHandler)
		want    bool
	}{
		{name: "a status write", deliver: updated(held, asObject(t, peripheralAt(1, false))), want: false},
		{name: "a spec edit", deliver: updated(held, asObject(t, peripheralAt(2, false))), want: true},
		{name: "a deletion request", deliver: updated(held, asObject(t, peripheralAt(1, true))), want: true},
		{name: "a removed object", deliver: removed(asObject(t, peripheralAt(1, true))), want: true},
		{
			name:    "an object removed while the watch was down",
			deliver: removed(cache.DeletedFinalStateUnknown{Key: "a0-ab-51-33-b7-12", Obj: held}),
			want:    true,
		},
		{name: "a new object", deliver: added(asObject(t, peripheralAt(1, false))), want: true},
		{name: "an object deleted and created again with the same name", deliver: updated(held, asObject(t, recreated)), want: true},
		{
			name:    "a removal whose tombstone holds no copy",
			deliver: removed(cache.DeletedFinalStateUnknown{Key: "a0-ab-51-33-b7-12"}),
			want:    true,
		},
		{name: "a held copy that did not convert", deliver: updated("not an object", held), want: true},
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

// The Adapter watch covers the whole cluster, and an Adapter wakes the
// loop only when this pod acts on it: its radio is the one this pod
// holds, its status names this node, or no node has written its status
// yet.
func TestAnAdapterEditWakesTheLoopOnlyOnItsNode(t *testing.T) {
	cases := []struct {
		name    string
		adapter Adapter
		want    bool
	}{
		{name: "the radio this pod holds", adapter: adapterOn(testAdapterName, "node-2", 2), want: true},
		{name: "another radio on this node", adapter: adapterOn("00-1a-7d-da-71-13", "node-1", 2), want: true},
		{name: "a radio no node holds yet", adapter: adapterOn("00-1a-7d-da-71-13", "", 2), want: true},
		{name: "a radio on another node", adapter: adapterOn("00-1a-7d-da-71-13", "node-2", 2), want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			watch := &editWatch{nodeName: "node-1", moved: make(chan struct{}, 1)}
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

// editServers answers the Adapter and the Peripheral collections, each
// from its own scripted server.
func editServers(t *testing.T, adapters, peripherals *watchServer) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(adaptersPath(), adapters.handler(t))
	mux.Handle(peripheralsPath(), peripherals.handler(t))
	return mux
}

// runEditWatch runs the edit watcher until the test ends.
func runEditWatch(t *testing.T, handler http.Handler) *editWatch {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	watch := watchEdits(ctx, testWatcher(t, handler), "node-1")
	t.Cleanup(func() {
		cancel()
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

// quiet is how long a test waits to see that no wake comes.
const quiet = 300 * time.Millisecond

// The operator's own status write reaches the watch as an event, and
// must not wake the loop, or every pass that writes a battery level
// would run a second pass. A person's edit to the spec wakes it. The
// server holds each event back until the test has read the wakes
// before it.
func TestAPeripheralEditWakesTheLoopAndAStatusWriteDoesNot(t *testing.T) {
	adapters := newWatchServer(adaptersPath(), "AdapterList", []string{"[]"})
	peripherals := newWatchServer(peripheralsPath(), "PeripheralList", []string{"[" + encode(t, peripheralAt(1, false)) + "]"}, []string{
		pause, peripheralEvent(t, "2", peripheralAt(1, false)),
		pause, peripheralEvent(t, "3", peripheralAt(2, false)),
		holdOpen,
	})
	watch := runEditWatch(t, editServers(t, adapters, peripherals))
	watch.follow(testAdapterName)
	adapters.awaitWatches(t, 1)
	peripherals.awaitWatches(t, 1)

	// Each watch wakes the loop when it starts.
	awaitWake(t, watch.wakes(), time.Second)
	settleWakes(watch.wakes(), quiet)
	peripherals.release()
	if wokeWithin(watch.wakes(), 2*quiet) {
		t.Fatal("the status write woke the loop")
	}
	peripherals.release()
	awaitWake(t, watch.wakes(), 3*time.Second)
}

// Each watch wakes the loop once when its first read is done, even
// when the read holds no object. At a start, the pass can read the
// objects before the watch does, and an edit made between the two
// reads is in the watch's read and in no event.
func TestAWatchWakesTheLoopWhenItsFirstReadIsDone(t *testing.T) {
	adapters := newWatchServer(adaptersPath(), "AdapterList", []string{"[]"})
	peripherals := newWatchServer(peripheralsPath(), "PeripheralList", []string{"[]"})
	watch := runEditWatch(t, editServers(t, adapters, peripherals))
	adapters.awaitWatches(t, 1)
	awaitWake(t, watch.wakes(), time.Second)
	settleWakes(watch.wakes(), quiet)

	watch.follow(testAdapterName)
	peripherals.awaitWatches(t, 1)
	awaitWake(t, watch.wakes(), time.Second)
}

// A watch that closes at once makes the reflector read the collection
// again after its backoff. The informer reports what changed between
// the two reads, so an edit made while the watch was down wakes the
// loop, and a status write made in the same time does not.
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
			adapters := newWatchServer(adaptersPath(), "AdapterList", []string{"[]"})
			reads := []string{"[" + encode(t, peripheralAt(1, false)) + "]", "[" + encode(t, c.after) + "]"}
			peripherals := newWatchServer(peripheralsPath(), "PeripheralList", reads, []string{}, []string{holdOpen})
			watch := runEditWatch(t, editServers(t, adapters, peripherals))
			watch.follow(testAdapterName)
			peripherals.awaitWatches(t, 1)
			settleWakes(watch.wakes(), quiet)

			peripherals.awaitWatches(t, 1)
			if got := wokeWithin(watch.wakes(), quiet); got != c.want {
				t.Fatalf("the change woke the loop: %t, want %t", got, c.want)
			}
		})
	}
}

// The pass reports the radio it holds on every pass. The same address
// keeps the Peripheral watch it has. A new address closes the watch for
// the old one and opens a watch that selects the new radio's
// Peripherals.
func TestThePeripheralWatchFollowsTheRadio(t *testing.T) {
	adapters := newWatchServer(adaptersPath(), "AdapterList", []string{"[]"})
	peripherals := newWatchServer(peripheralsPath(), "PeripheralList", []string{"[]"})
	watch := runEditWatch(t, editServers(t, adapters, peripherals))
	const other = "00-1a-7d-da-71-13"

	watch.follow(testAdapterName)
	peripherals.awaitWatches(t, 1)
	watch.follow(testAdapterName)
	watch.follow(other)
	peripherals.awaitWatches(t, 1)

	want := fmt.Sprint([]string{adapterSelector(testAdapterName), adapterSelector(other)})
	deadline := time.After(5 * time.Second)
	for {
		holding, selectors := peripherals.held()
		if holding == 1 && fmt.Sprint(selectors) == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("the server holds %d watches, and the requests selected %v; want 1 and %s", holding, selectors, want)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// The pass reports the address of the radio it read, so the edit
// watcher selects that radio's Peripherals.
func TestAPassReportsTheRadioItHolds(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))
	var followed []string
	inventory.follow = func(key string) { followed = append(followed, key) }

	inventory.reconcile()

	if fmt.Sprint(followed) != "["+testAdapterName+"]" {
		t.Fatalf("the pass reported %v, want [%s]", followed, testAdapterName)
	}
}
