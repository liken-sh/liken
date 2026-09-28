package main

// The stores a pass reads, and the requests a pass sends with and
// without them.

import (
	"context"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"
)

// heldStore is a store that holds the given objects, synced or not.
func heldStore(t *testing.T, synced bool, objects ...*unstructured.Unstructured) *watchStore {
	t.Helper()
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	for _, object := range objects {
		mustSucceed(t, store.Add(object))
	}
	held := &watchStore{}
	held.hold(store, func() bool { return synced })
	return held
}

// A store answers a pass only while its informer runs and has finished
// its first read, and only when every object converts. Otherwise the
// pass reads the API server.
func TestAStoreAnswersOnlyWhenItHoldsTheWholeCollection(t *testing.T) {
	theater, lounge := asObject(t, receiverAt("uid-1", 1, "")), asObject(t, receiverAt("uid-2", 1, ""))
	lounge.SetName("lounge")
	mistyped := asObject(t, receiverAt("uid-3", 1, ""))
	mistyped.SetName("attic")
	_ = unstructured.SetNestedField(mistyped.Object, "one", "metadata", "generation")
	released := heldStore(t, true, theater)
	released.release()
	cases := []struct {
		name  string
		held  *watchStore
		ok    bool
		names []string
	}{
		{"no watch", nil, false, nil},
		{"a watch that has not finished its first read", heldStore(t, false, theater), false, nil},
		{"a watch that stopped", released, false, nil},
		{"a watch that has read", heldStore(t, true, theater, lounge), true, []string{"lounge", "theater"}},
		{"an object that does not convert", heldStore(t, true, theater, mistyped), false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			items, ok := cachedList[Receiver](c.held, "the Receivers")

			mustMatch(t, ok, c.ok)
			var names []string
			for _, item := range items {
				names = append(names, item.Metadata.Name)
			}
			mustDeepEqual(t, names, c.names)
		})
	}
}

// A read of one object from the store answers the object, or not found,
// and reads the API server while the store has nothing to give.
func TestADisplayIsReadFromTheStore(t *testing.T) {
	api := startCECAPI(t)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	held := heldStore(t, true, asObject(t, displayAt("uid-1", "node-2", "2.0.0.0")))
	mistyped := asObject(t, displayAt("uid-2", "node-1", "3.0.0.0"))
	mistyped.SetName("acm-0001-receiver")
	_ = unstructured.SetNestedField(mistyped.Object, int64(3), "status", "physicalAddress")
	cases := []struct {
		name    string
		held    *watchStore
		display string
		address string
		missing bool
		reads   int
	}{
		{"from the store", held, "den-hdmi-1", "2.0.0.0", false, 0},
		{"absent from the store", held, "acm-0001-receiver", "", true, 0},
		{"from the API server with no watch", nil, "acm-0001-receiver", "1.3.0.0", false, 1},
		{"from the API server when the stored copy does not convert", heldStore(t, true, mistyped), "acm-0001-receiver", "1.3.0.0", false, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := api.readCount()

			display, err := readDisplay(api.client, c.held, c.display)

			mustMatch(t, err == ErrNotFound, c.missing)
			if !c.missing {
				mustMatch(t, display.Status.PhysicalAddress, c.address)
			}
			mustMatch(t, api.readCount()-before, c.reads)
		})
	}
}

// readCount answers how many reads the fake has answered.
func (a *cecAPI) readCount() int {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	total := 0
	for _, count := range a.reads {
		total += count
	}
	return total
}

// readsUnder answers how many reads of one collection, the list or
// one of its objects, the fake has answered.
func (a *cecAPI) readsUnder(collection string) int {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	total := 0
	for path, count := range a.reads {
		if path == collection || strings.HasPrefix(path, collection+"/") {
			total += count
		}
	}
	return total
}

// awaitStores waits until every store has finished its first read.
func awaitStores(t *testing.T, stores ...*watchStore) {
	t.Helper()
	deadline := time.After(testTimeout)
	for _, held := range stores {
		for {
			if _, ok := held.current(); ok {
				break
			}
			select {
			case <-deadline:
				t.Fatal("a watch did not finish its first read")
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
}

// startWatches runs watch functions against the fake until the test
// ends, each into its own store.
func startWatches(t *testing.T, client *Client, watches map[*watchStore]watchFunc) {
	t.Helper()
	for held, watch := range watches {
		runHeldWatch(t, client, watch, nil, held)
	}
	for held := range watches {
		awaitStores(t, held)
	}
}

// The Deployment's CECBus pass reads the CECBuses, the Televisions, the
// Displays, and the Receivers. It writes the first two kinds, so it
// lists them from the API server on every pass. It writes neither of
// the other two, so once their watches have read, it reads their
// stores.
func TestTheCECBusPassReadsTheStoresOfWhatItDoesNotWrite(t *testing.T) {
	cases := []struct {
		name    string
		watched bool
		reads   map[string]int
	}{
		{"no watch", false, map[string]int{cecBusesPath: 1, televisionsPath: 1, displaysPath: 1, receiversPath: 1}},
		{"the watches have read", true, map[string]int{cecBusesPath: 1, televisionsPath: 1, displaysPath: 0, receiversPath: 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := startCECAPI(t)
			api.putBus(scannedBus("den", tvDevice, receiverDevice))
			api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
			api.putReceiver(wiredReceiver("den", "node-1", "acm-0001-receiver"))
			api.putTelevision(Television{Metadata: ObjectMeta{Name: "lounge"}, Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: "den"}}})
			controller := newCECBusController(api.client)
			if c.watched {
				startWatches(t, api.client, map[*watchStore]watchFunc{
					controller.displays: watchDisplays, controller.receivers: watchReceiverSpecs,
				})
			}
			before := map[string]int{}
			for path := range c.reads {
				before[path] = api.readsUnder(path)
			}

			mustSucceed(t, controller.pass())

			for path, want := range c.reads {
				mustMatch(t, api.readsUnder(path)-before[path], want)
			}
			television, _ := api.television("lounge")
			mustDeepEqual(t, television.Status.Displays, []TelevisionDisplay{
				{Name: "acm-0001-receiver", PhysicalAddress: "1.3.0.0", Via: &EquipmentRef{Kind: "Receiver", Name: "den"}},
			})
		})
	}
}

// The node workload in Control reads the Display its adapter speaks
// for from the Display watch's store: a change that wakes a pass sends
// no read of a Display to the API server.
func TestTheNodePassReadsTheDisplayFromTheStore(t *testing.T) {
	api := startCECAPI(t)
	_, device := usbAdapter(cecRoom())
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	node, err := newCECNode(api.client, "node-1", device)
	mustSucceed(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = node.run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
	})
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	awaitStores(t, node.displays)
	before, passes := api.readsUnder(displaysPath), api.readsUnder(cecBusesPath)

	for range 3 {
		api.nudge()
		time.Sleep(watchQuiet / 3)
	}

	if api.readsUnder(cecBusesPath) == passes {
		t.Fatal("no pass ran")
	}
	mustMatch(t, api.readsUnder(displaysPath)-before, 0)
}
