package main

// These tests count the requests that an inventory pass sends when it
// reads from the watch stores, and check the writes that a pass makes
// from a store copy older than the API server's.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// storeOf returns a ready store with copies of the fixture's objects of
// one kind. A later write to the fixture leaves the store's copy older
// than the API server's.
func storeOf(t *testing.T, fixture *apiFixture, kind string) informer.View {
	t.Helper()
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	for _, path := range sortedPaths(fixture.objects) {
		object := fixture.objects[path]
		if object["kind"] != kind {
			continue
		}
		raw, err := json.Marshal(object)
		if err != nil {
			t.Fatalf("encoding the %s object: %v", kind, err)
		}
		item := &unstructured.Unstructured{}
		if err := item.UnmarshalJSON(raw); err != nil {
			t.Fatalf("decoding the %s object: %v", kind, err)
		}
		if err := store.Add(item); err != nil {
			t.Fatalf("adding the %s object to the store: %v", kind, err)
		}
	}
	return informer.View{Store: store, Synced: func() bool { return true }}
}

// cacheOf returns an objectCache whose stores hold the fixture's
// objects as they are now, with the Peripheral store selected for radio.
// It sets no memo, so the pass treats every store copy as current, even
// after the fixture changes.
func cacheOf(t *testing.T, fixture *apiFixture, radio string) objectCache {
	t.Helper()
	peripherals := &followedView{}
	peripherals.set(radio, storeOf(t, fixture, peripheralKind))
	return objectCache{
		adapters:    informer.Held{View: storeOf(t, fixture, adapterKind)},
		requests:    informer.Held{View: storeOf(t, fixture, pairingRequestKind)},
		peripherals: peripherals,
	}
}

// sent returns the requests of one method that the fixture received,
// and clears the fixture's list of requests.
func sent(fixture *apiFixture, method string) []string {
	var matched []string
	for _, request := range fixture.requests {
		if strings.HasPrefix(request, method+" ") {
			matched = append(matched, request)
		}
	}
	fixture.requests = nil
	return matched
}

// finishedRequest returns a request that has paired its device, so a
// pass lists it and takes no action on it.
func finishedRequest() *PairingRequest {
	request := openRequest(testDevice)
	request.Status = PairingRequestStatus{Phase: phasePaired, FinishedAt: timestamp(testNow)}
	return request
}

// A pass that changes nothing reads the Adapter and lists the Adapters,
// the Peripherals, and the PairingRequests. That is four GET requests
// against the API server and none against the stores. A Peripheral
// store selected for another radio is not used, so the pass sends one
// GET to list the Peripherals.
func TestAPassFromTheStoresSendsNoRead(t *testing.T) {
	for _, c := range []struct {
		name  string
		cache func(t *testing.T, fixture *apiFixture) objectCache
		want  int
	}{
		{"the API server", func(*testing.T, *apiFixture) objectCache { return objectCache{} }, 4},
		{"the stores", func(t *testing.T, f *apiFixture) objectCache { return cacheOf(t, f, testAdapterName) }, 0},
		{"the stores, with the Peripherals of another radio", func(t *testing.T, f *apiFixture) objectCache {
			return cacheOf(t, f, "00-1a-7d-da-71-13")
		}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			fixture := newAPIFixture()
			fixture.put(t, testRequestPath(), finishedRequest())
			inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))
			inventory.reconcile()
			inventory.cache = c.cache(t, fixture)
			fixture.requests = nil

			if pass := inventory.reconcile(); !pass.ok {
				t.Fatal("the reconcile pass of settled state reported a failure")
			}
			if reads := sent(fixture, http.MethodGet); len(reads) != c.want {
				t.Errorf("the pass sent %d reads, want %d: %v", len(reads), c.want, reads)
			}
		})
	}
}

// When the store's Adapter is older than the API server's, the API
// server refuses the status write with a conflict. The pass then reads
// the Adapter again and writes the status to the fresh copy.
func TestAnAdapterStatusFromAnOlderCopyIsWrittenAfterReadingAgain(t *testing.T) {
	fixture := newAPIFixture()
	radio := testRadio(t, pairedDevice(t, testDevice))
	inventory := testInventory(t, fixture, radio)
	inventory.reconcile()
	inventory.cache = cacheOf(t, fixture, testAdapterName)
	// Write the Adapter again to move its resourceVersion past the
	// store's copy.
	fixture.put(t, testAdapterObjectPath(), read[Adapter](t, fixture, testAdapterObjectPath()))
	radio.snapshot.Adapter.Powered = false

	if pass := inventory.reconcile(); !pass.ok {
		t.Fatal("the reconcile pass reported a failure")
	}
	if adapter := read[Adapter](t, fixture, testAdapterObjectPath()); adapter.Status.Powered {
		t.Error("the Adapter status still shows powered, want unpowered after the radio powered off")
	}
}

// When removing a departed Adapter's finalizer fails with a conflict,
// the pass reads the Adapter again. It removes the finalizer only when
// the fresh copy still has this node in status.node, because another
// node may have adopted the radio.
func TestTheReleaseRechecksAnAdapterAfterAConflict(t *testing.T) {
	const departed = "00-1a-7d-da-71-13"
	for _, c := range []struct {
		name         string
		node         string
		wantReleased bool
	}{
		{"another node took it", "node-2", false},
		{"it is still this node's", "liken-1", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			fixture := newAPIFixture()
			adapter := adapterOn(departed, "liken-1", 1)
			adapter.Metadata.Finalizers = []string{adapterFinalizer}
			adapter.Metadata.DeletionTimestamp = timestamp(testNow)
			fixture.put(t, adapterPath(departed), adapter)
			inventory := testInventory(t, fixture, testRadio(t))
			inventory.cache = cacheOf(t, fixture, testAdapterName)
			adapter.Status.Node = c.node
			fixture.put(t, adapterPath(departed), adapter)

			inventory.releaseDepartedAdapters(testAdapterAddress(t))

			_, exists := fixture.objects[adapterPath(departed)]
			if released := !exists; released != c.wantReleased {
				t.Errorf("the Adapter was released: %t, want %t", released, c.wantReleased)
			}
		})
	}
}

// The second pass writes the disconnect and counts it. The third pass
// starts from the same store copy, which is now older than that write,
// so its status write fails with a conflict. It reads the Peripheral
// again, finds the disconnect already written, and does not count it.
func TestAnOlderCopyNeverCountsADisconnectTwice(t *testing.T) {
	fixture := newAPIFixture()
	radio := testRadio(t, pairedDevice(t, testDevice))
	inventory := testInventory(t, fixture, radio)
	inventory.reconcile()
	inventory.cache = cacheOf(t, fixture, testAdapterName)
	radio.update(testAddress(t, testDevice), func(d *deviceState) { d.Connected = false })

	inventory.reconcile()
	inventory.reconcile()

	count, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_disconnects_total", map[string]string{"peripheral": "a0-ab-51-33-b7-12", "reason": reasonNotConnected})
	if !found || count != 1 {
		t.Errorf("bluetooth_disconnects_total = %v (found: %v), want 1", count, found)
	}
	peripheral := read[Peripheral](t, fixture, testPeripheralPath())
	if peripheral.Status.Bond.Connected {
		t.Error("the Peripheral status still shows connected, want disconnected after the radio dropped the connection")
	}
}

// The store can still hold a Peripheral after the operator removed its
// finalizer. The teardown reads the Peripheral from the API server, so
// it does not remove a device that no Peripheral refers to from the
// slice.
func TestATeardownFromTheCopyOfAReleasedPeripheralDoesNothing(t *testing.T) {
	fixture := newAPIFixture()
	device := pairedDevice(t, testDevice)
	device.Connected = false
	inventory := testInventory(t, fixture, testRadio(t, device))
	inventory.reconcile()
	deletePeripheral(t, fixture)
	inventory.cache = cacheOf(t, fixture, testAdapterName)
	delete(fixture.objects, testPeripheralPath())

	pass := inventory.reconcile()

	if len(pass.excluded) != 0 || len(pass.unpairing) != 0 {
		t.Errorf("the pass tore down a Peripheral that is gone, want no teardown: keepOut %v, unpairing %v", pass.excluded, pass.unpairing)
	}
}

// A request status write from an older copy fails with a conflict.
// When the API server's copy still has the status the pass started
// from, a spec edit caused the conflict, and the pass writes its status
// to the fresh copy. When the API server's copy has a different status,
// the pass writes nothing and asks for another pass.
func TestARequestStatusFromAnOlderCopy(t *testing.T) {
	for _, c := range []struct {
		name      string
		newer     func(request *PairingRequest)
		wantPhase string
	}{
		{"after a spec edit", func(r *PairingRequest) { r.Spec.WindowSeconds = 240 }, phaseOpen},
		{"after this operator's own write", func(r *PairingRequest) {
			r.Status = PairingRequestStatus{Phase: phaseExpired, FinishedAt: timestamp(testNow)}
		}, phaseExpired},
	} {
		t.Run(c.name, func(t *testing.T) {
			fixture := newAPIFixture()
			fixture.put(t, testRequestPath(), openRequest(""))
			inventory := testInventory(t, fixture, testRadio(t))
			inventory.cache = cacheOf(t, fixture, testAdapterName)
			request := read[PairingRequest](t, fixture, testRequestPath())
			c.newer(request)
			fixture.put(t, testRequestPath(), request)

			pass := inventory.reconcile()

			if got := read[PairingRequest](t, fixture, testRequestPath()).Status.Phase; got != c.wantPhase {
				t.Errorf("the request's phase = %q, want %q", got, c.wantPhase)
			}
			if pass.again == 0 {
				t.Error("the pass scheduled no follow-up pass, want one")
			}
		})
	}
}

// A list before the PairingRequest store is ready comes from the API
// server. The store can then become ready with an older copy, because
// the API server can serve the watch's first list from its watch cache.
// If the next list returned that copy, a pass would see a finished
// request as unfinished and open the pairing window again.
func TestAListBeforeTheStoreIsReadyIsNeverReadOlder(t *testing.T) {
	fixture := newAPIFixture()
	fixture.put(t, testRequestPath(), openRequest(""))
	inventory := testInventory(t, fixture, testRadio(t))
	older := storeOf(t, fixture, pairingRequestKind)
	fixture.put(t, testRequestPath(), finishedRequest())
	ready := false
	inventory.cache.requests = informer.Held{View: informer.View{Store: older.Store, Synced: func() bool { return ready }}, Versions: memo.New()}

	listed, err := inventory.listRequests()
	if err != nil || len(listed) != 1 || listed[0].Status.Phase != phasePaired {
		t.Fatalf("the list before the store is ready = %+v, %v; want the paired request", listed, err)
	}
	ready = true
	read, err := inventory.listRequests()

	if err != nil || len(read) != 1 || read[0].Status.Phase != phasePaired {
		t.Errorf("the list from the ready store = %+v, %v; want the paired request, not the store's older copy", read, err)
	}
}

// The memo records the version of the operator's own status write, so
// the next pass reads the paired request from the API server instead of
// the store's older copy, and does not open the pairing window again.
func TestAPassDoesNotActOnACopyOlderThanItsOwnWrite(t *testing.T) {
	fixture := newAPIFixture()
	fixture.put(t, testRequestPath(), openRequest(testDevice))
	radio := testRadio(t, seenDevice(t, testDevice, "DualSense Wireless Controller"))
	inventory := testInventory(t, fixture, radio)
	inventory.cache = cacheOf(t, fixture, testAdapterName)
	inventory.cache.requests.Versions = memo.New()
	inventory.reconcile()
	if request := read[PairingRequest](t, fixture, testRequestPath()); request.Status.Phase != phasePaired {
		t.Fatalf("after the first pass the request phase is %q, want %q", request.Status.Phase, phasePaired)
	}

	radio.calls = nil
	inventory.reconcile()

	for _, call := range radio.calls {
		if strings.HasPrefix(call, "OpenWindow") {
			t.Fatalf("the pass opened the pairing window again for a paired request: %v", radio.calls)
		}
	}
}
