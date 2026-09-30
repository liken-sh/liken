package main

// The stores a pass reads, and the requests a pass sends with and
// without them.

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
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
	held.hold(informer.View{Store: store, Synced: func() bool { return synced }}, false)
	return held
}

// A pass reads a store only while its informer runs and has finished
// its first read, and lists from the API server otherwise. A copy in
// the store that does not convert is read from the API server, so the
// pass leaves out no object.
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
		lists int
		gets  int
		names []string
	}{
		{"no watch", nil, 1, 0, []string{"attic", "lounge", "theater"}},
		{"a watch that has not finished its first read", heldStore(t, false, theater), 1, 0, []string{"attic", "lounge", "theater"}},
		{"a watch that stopped", released, 1, 0, []string{"attic", "lounge", "theater"}},
		{"a watch that has read", heldStore(t, true, theater, lounge), 0, 0, []string{"lounge", "theater"}},
		{"an object that does not convert", heldStore(t, true, theater, mistyped), 0, 1, []string{"attic", "theater"}},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := startCECAPI(t)
				for _, name := range []string{"attic", "lounge", "theater"} {
					receiver := receiverAt("uid-"+name, 1, "")
					receiver.Metadata.Name = name
					api.putReceiver(receiver)
				}
				lists, gets := api.readCountOf(receiversPath), api.readsUnder(receiversPath)-api.readCountOf(receiversPath)

				list, err := readReceivers(api.client, c.held)

				mustSucceed(t, err)
				var names []string
				for _, item := range list.Items {
					names = append(names, item.Metadata.Name)
				}
				mustDeepEqual(t, names, c.names)
				mustMatch(t, api.readCountOf(receiversPath)-lists, c.lists)
				mustMatch(t, api.readsUnder(receiversPath)-api.readCountOf(receiversPath)-gets, c.gets)
			})
		})
	}
}

// A read of one object from the store answers the object, or not found,
// and reads the API server while the store has nothing to give.
func TestADisplayIsReadFromTheStore(t *testing.T) {
	t.Parallel()
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

			mustMatch(t, err == apiclient.ErrNotFound, c.missing)
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

// readCountOf answers how many reads of one path, a list or one
// object, the fake has answered.
func (a *cecAPI) readCountOf(path string) int {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.reads[path]
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
			if held.view().Ready() {
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

// delivered waits until a store holds the copy of each object the
// memo noted, which is when the watch has delivered every write. With
// no watch there is nothing to wait for.
func delivered(t *testing.T, held *watchStore, versions *memo.Versions) {
	t.Helper()
	store := held.view().Store
	if store == nil {
		return
	}
	deadline := time.After(testTimeout)
	for {
		waiting := len(versions.Unheld(store)) > 0
		for _, object := range store.List() {
			copied := object.(*unstructured.Unstructured)
			if !versions.Current(copied.GetName(), copied.GetResourceVersion()) {
				waiting = true
			}
		}
		if !waiting {
			return
		}
		select {
		case <-deadline:
			t.Fatal("a watch did not deliver the pass's writes")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// The Deployment's CECBus pass reads the CECBuses, the Televisions, the
// Displays, and the Receivers. With no watch it lists each kind. Once
// the watches have read, it reads their stores, and a settled pass, one
// that runs after the watches delivered the last pass's writes, sends
// the API server no request.
func TestTheCECBusPassReadsTheStores(t *testing.T) {
	cases := []struct {
		name    string
		watched bool
		reads   map[string]int
	}{
		{"no watch", false, map[string]int{cecBusesPath: 1, televisionsPath: 1, displaysPath: 1, receiversPath: 1}},
		{"the watches have read", true, map[string]int{cecBusesPath: 0, televisionsPath: 0, displaysPath: 0, receiversPath: 0}},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := startCECAPI(t)
				api.putBus(scannedBus("den", tvDevice, receiverDevice))
				api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
				api.putReceiver(wiredReceiver("den", "node-1", "acm-0001-receiver"))
				api.putTelevision(Television{Metadata: ObjectMeta{Name: "lounge"}, Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: "den"}}})
				controller := newCECBusController(api.client)
				if c.watched {
					startWatches(t, api.client, map[*watchStore]watchFunc{
						controller.buses: watchCECBuses, controller.televisions: watchTelevisions,
						controller.displays: watchDisplays, controller.receivers: watchReceiverSpecs,
					})
				}
				mustSucceed(t, controller.pass())
				delivered(t, controller.buses, api.client.versions.cecBuses)
				delivered(t, controller.televisions, api.client.versions.televisions)
				before := map[string]int{}
				for path := range c.reads {
					before[path] = api.readsUnder(path)
				}
				writes := api.derivedWrites

				mustSucceed(t, controller.pass())

				for path, want := range c.reads {
					mustMatch(t, api.readsUnder(path)-before[path], want)
				}
				mustMatch(t, api.derivedWrites, writes)
				television, _ := api.television("lounge")
				mustDeepEqual(t, television.Status.Displays, []TelevisionDisplay{
					{Name: "acm-0001-receiver", PhysicalAddress: "1.3.0.0", Via: &EquipmentRef{Kind: "Receiver", Name: "den"}},
				})
			})
		})
	}
}

// runNode runs the node workload for node-1 until the test ends, and
// answers it once its entry reports the scan and its watches hold
// their first reads.
func runNode(t *testing.T, api *cecAPI) *cecNode {
	t.Helper()
	_, device := usbAdapter(cecRoom())
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
	api.scanned(t, "node-1")
	awaitStores(t, node.buses, node.displays)
	return node
}

// The node workload in Control reads the CECBuses, the Televisions, and
// the Display its adapter speaks for from the watches' stores. A pass
// that a Display's move wakes announces the new address, and lists
// nothing and reads no Display from the API server. A pass that runs
// before the watch delivered the node's own entry write reads that
// CECBus from the API server once, so those reads are not counted.
func TestTheNodePassReadsFromTheStores(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		runNode(t, api)
		lists := func() []int {
			return []int{api.readCountOf(cecBusesPath), api.readCountOf(televisionsPath), api.readsUnder(displaysPath)}
		}
		before := lists()

		api.moveDisplay("acm-0001-receiver", "2.0.0.0")

		api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.PhysicalAddress == "2.0.0.0" })
		mustDeepEqual(t, lists(), before)
	})
}

// The node workload watches the Displays of its own machine, by the
// field selector on status.node, so its store holds no other machine's
// Display.
func TestTheNodeWatchesTheDisplaysOfItsMachine(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putDisplay("acm-0002-receiver", "node-2", "2.0.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))

		node := runNode(t, api)

		view := node.displays.view()
		mustDeepEqual(t, view.Store.ListKeys(), []string{"acm-0001-receiver"})
		mustMatch(t, view.Whole, false)
	})
}

// An API server whose Display definition declares no selectable field
// on status.node refuses the node workload's Display list. The node
// workload still starts, and reads the Display it speaks for from the
// API server.
func TestTheNodeStartsWhenTheDisplayListIsRefused(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.noDisplayNodeField = true
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		_, device := usbAdapter(cecRoom())

		startNode(t, api, "node-1", device)

		entry := api.scanned(t, "node-1")
		mustMatch(t, entry.PhysicalAddress, "1.3.0.0")
	})
}
