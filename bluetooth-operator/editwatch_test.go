package main

// These tests cover what wakes the loop for an Adapter or a Peripheral:
// a spec edit, a deletion request, a new object, and a removed object
// wake it. A status write, and an Adapter that another node holds, do
// not. The Peripheral watch follows the radio the pass reports.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

func peripheralAt(generation int64, deleting bool) Peripheral {
	peripheral := Peripheral{Metadata: ObjectMeta{Name: "a0-ab-51-33-b7-12", Generation: generation}}
	if deleting {
		peripheral.Metadata.DeletionTimestamp = timestamp(testNow)
	}
	return peripheral
}

func adapterOn(name, node string, generation int64) Adapter {
	return Adapter{
		Metadata: ObjectMeta{Name: name, Generation: generation},
		Status:   AdapterStatus{Node: node},
	}
}

// Each case starts from a list that holds one object at generation 1,
// and delivers one event.
func TestAnEventWakesTheLoopOnlyForAnEdit(t *testing.T) {
	cases := []struct {
		name  string
		event string
		item  Peripheral
		want  bool
	}{
		{name: "a status write", event: "MODIFIED", item: peripheralAt(1, false), want: false},
		{name: "a spec edit", event: "MODIFIED", item: peripheralAt(2, false), want: true},
		{name: "a deletion request", event: "MODIFIED", item: peripheralAt(1, true), want: true},
		{name: "a removed object", event: "DELETED", item: peripheralAt(1, true), want: true},
		{
			name:  "a new object",
			event: "ADDED",
			item:  Peripheral{Metadata: ObjectMeta{Name: "e3-28-e9-23-21-6f", Generation: 1}},
			want:  true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wakes := 0
			tracker := &editTracker[Peripheral]{wake: func() { wakes++ }, mark: peripheralMark}
			tracker.replace([]Peripheral{peripheralAt(1, false)})
			tracker.apply(c.event, c.item)
			if got := wakes == 2; got != c.want {
				t.Fatalf("the event woke the loop: %t, want %t", got, c.want)
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
			tracker := &editTracker[Adapter]{wake: func() { wakes++ }, mark: watch.adapterMark}
			before := c.adapter
			before.Metadata.Generation = 1
			tracker.replace([]Adapter{before})
			tracker.apply("MODIFIED", c.adapter)
			if got := wakes == 2; got != c.want {
				t.Fatalf("the edit woke the loop: %t, want %t", got, c.want)
			}
		})
	}
}

// editServers answers the Adapter and the Peripheral collections, each
// from its own scripted server.
func editServers(adapters, peripherals *watchServer) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(adaptersPath(), adapters)
	mux.Handle(peripheralsPath(), peripherals)
	return mux
}

// runEditWatch runs the edit watcher until the test ends.
func runEditWatch(t *testing.T, handler http.Handler) *editWatch {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	watch := watchEdits(ctx, testClient(t, handler), "node-1")
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
	encoded, err := json.Marshal(peripheral)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`{"type":"MODIFIED","object":%s}`, encoded)
}

// The operator's own status write reaches the watch as an event, and
// must not wake the loop, or every pass that writes a battery level
// would run a second pass. A person's edit to the spec wakes it. The
// server holds the spec edit back until the test has read the wakes
// after the status write.
func TestAPeripheralEditWakesTheLoopAndAStatusWriteDoesNot(t *testing.T) {
	adapters := newWatchServer(adaptersPath(), "[]", []string{holdOpen})
	listed, err := json.Marshal([]Peripheral{peripheralAt(1, false)})
	if err != nil {
		t.Fatal(err)
	}
	peripherals := newWatchServer(peripheralsPath(), string(listed), []string{
		peripheralEvent(t, "2", peripheralAt(1, false)),
		pause, peripheralEvent(t, "3", peripheralAt(2, false)),
		holdOpen,
	})
	watch := runEditWatch(t, editServers(adapters, peripherals))
	watch.follow(testAdapterName)
	adapters.awaitWatches(t, 1)
	peripherals.awaitWatches(t, 1)

	// Each list wakes the loop once. Read that wake before the events.
	awaitWake(t, watch.wakes(), time.Second)
	select {
	case <-watch.wakes():
		t.Fatal("the status write woke the loop")
	case <-time.After(500 * time.Millisecond):
	}
	peripherals.release()
	awaitWake(t, watch.wakes(), 3*time.Second)
}

// The pass reports the radio it holds on every pass. The same address
// keeps the Peripheral watch it has. A new address closes the watch for
// the old one and opens a watch that selects the new radio's
// Peripherals.
func TestThePeripheralWatchFollowsTheRadio(t *testing.T) {
	adapters := newWatchServer(adaptersPath(), "[]", []string{holdOpen})
	peripherals := newWatchServer(peripheralsPath(), "[]", []string{holdOpen}, []string{holdOpen})
	watch := runEditWatch(t, editServers(adapters, peripherals))
	const other = "00-1a-7d-da-71-13"

	watch.follow(testAdapterName)
	peripherals.awaitWatches(t, 1)
	watch.follow(testAdapterName)
	watch.follow(other)
	peripherals.awaitWatches(t, 1)

	label := func(key string) string { return bonds.AdapterLabel + "=" + key }
	want := fmt.Sprint([]string{label(testAdapterName), label(testAdapterName), label(other), label(other)})
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
