package main

// These tests count the requests an inventory pass sends when it reads
// the Adapters, the Peripherals, and the PairingRequests from the
// watches' stores, and cover each write from a copy in a store that is
// older than the API server's.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"
)

// storeOf is a store that holds the fixture's objects of one kind as
// they are now, the way a watch's store holds them once the watch has
// delivered every write. The store holds copies, so a later write to
// the fixture leaves the store older than the API server.
func storeOf(t *testing.T, fixture *apiFixture, kind string) storeView {
	t.Helper()
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	for _, path := range sortedPaths(fixture.objects) {
		object := fixture.objects[path]
		if object["kind"] != kind {
			continue
		}
		raw, err := json.Marshal(object)
		if err != nil {
			t.Fatal(err)
		}
		item := &unstructured.Unstructured{}
		if err := item.UnmarshalJSON(raw); err != nil {
			t.Fatal(err)
		}
		if err := store.Add(item); err != nil {
			t.Fatal(err)
		}
	}
	return storeView{store: store, synced: func() bool { return true }}
}

// cacheOf is the three stores of an inventory, holding what the
// fixture holds now. The Peripheral store is the one for radio. The
// stores carry no memo, so every copy in them counts as current, the
// way a copy that another writer changed since does.
func cacheOf(t *testing.T, fixture *apiFixture, radio string) objectCache {
	t.Helper()
	peripherals := &followedView{}
	peripherals.set(radio, storeOf(t, fixture, peripheralKind))
	return objectCache{
		adapters:    heldObjects{view: storeOf(t, fixture, adapterKind)},
		requests:    heldObjects{view: storeOf(t, fixture, pairingRequestKind)},
		peripherals: peripherals,
	}
}

// sent answers the requests of one method among the requests the
// fixture received, and forgets every request.
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

// finishedRequest is a request that paired its device a moment ago, so
// a pass lists it and has nothing to do with it.
func finishedRequest() *PairingRequest {
	request := openRequest(testDevice)
	request.Status = PairingRequestStatus{Phase: phasePaired, FinishedAt: timestamp(testNow)}
	return request
}

// A settled pass reads the Adapter, lists the Adapters, the
// Peripherals, and the PairingRequests, and writes nothing. From the
// API server that is four reads, and from the stores it is none. The
// Peripheral store follows the radio, and a store that holds another
// radio's Peripherals is not read.
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
				t.Fatal("the settled pass failed")
			}
			if reads := sent(fixture, http.MethodGet); len(reads) != c.want {
				t.Errorf("the pass sent %d reads, want %d: %v", len(reads), c.want, reads)
			}
		})
	}
}

// A store's copy of an Adapter can be older than this operator's own
// last status write. The write from it is refused, and the pass reads
// the Adapter again and writes once more, so the status lands.
func TestAnAdapterStatusFromAnOlderCopyReadsAgainAndLands(t *testing.T) {
	fixture := newAPIFixture()
	radio := testRadio(t, pairedDevice(t, testDevice))
	inventory := testInventory(t, fixture, radio)
	inventory.reconcile()
	inventory.cache = cacheOf(t, fixture, testAdapterName)
	// A write the store has not delivered.
	fixture.put(t, testAdapterObjectPath(), read[Adapter](t, fixture, testAdapterObjectPath()))
	radio.snapshot.Adapter.Powered = false

	if pass := inventory.reconcile(); !pass.ok {
		t.Fatal("the pass failed")
	}
	if adapter := read[Adapter](t, fixture, testAdapterObjectPath()); adapter.Status.Powered {
		t.Error("the status that the radio powered off did not land")
	}
}

// The release reads a departed Adapter again after a conflict. It
// leaves the Adapter when the fresh copy names another node, because
// that node adopted the radio since the store's copy, and releases it
// when the fresh copy is still this node's.
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

			_, held := fixture.objects[adapterPath(departed)]
			if released := !held; released != c.wantReleased {
				t.Errorf("the Adapter was released: %t, want %t", released, c.wantReleased)
			}
		})
	}
}

// A store's copy of a Peripheral can be older than this operator's own
// last status write. A drop the pass wrote from one copy is counted
// once, and a later pass from the same older copy reads the Peripheral
// again, finds the drop written, and counts nothing more.
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
		"bluetooth_disconnects_total", map[string]string{"peripheral": "a0-ab-51-33-b7-12"})
	if !found || count != 1 {
		t.Errorf("bluetooth_disconnects_total = %v (found: %v), want 1", count, found)
	}
	peripheral := read[Peripheral](t, fixture, testPeripheralPath())
	if peripheral.Status.Bond.Connected {
		t.Error("the drop did not land")
	}
}

// The teardown reads the Peripheral from the API server. The store can
// still hold a Peripheral whose finalizer this operator released, and a
// teardown from that copy would retire a device that no Peripheral
// names.
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

	if len(pass.keepOut) != 0 || len(pass.unpairing) != 0 {
		t.Errorf("the pass tore down a Peripheral that is gone: keepOut %v, unpairing %v", pass.keepOut, pass.unpairing)
	}
}

// A request's status from an older copy is refused. When the API
// server holds the status the pass composed from, a spec edit made the
// conflict, and the status lands on the fresh copy. When it holds a
// newer status, which is this operator's own last write, the pass
// writes nothing, and the follow-up pass composes from the newer one.
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
				t.Error("the pass asked for no follow-up pass")
			}
		})
	}
}

// The store can still hold a request as it was before this operator
// paired its device. The operator remembers the version its own write
// produced, reads the request from the API server instead of the
// store's older copy, and does not open the pairing window again for a
// request that is finished.
func TestAPassDoesNotActOnACopyOlderThanItsOwnWrite(t *testing.T) {
	fixture := newAPIFixture()
	fixture.put(t, testRequestPath(), openRequest(testDevice))
	radio := testRadio(t, seenDevice(t, testDevice, "DualSense Wireless Controller"))
	inventory := testInventory(t, fixture, radio)
	inventory.cache = cacheOf(t, fixture, testAdapterName)
	inventory.cache.requests.versions = newVersionMemo()
	inventory.reconcile()
	if request := read[PairingRequest](t, fixture, testRequestPath()); request.Status.Phase != phasePaired {
		t.Fatalf("the first pass left the request %q", request.Status.Phase)
	}

	radio.calls = nil
	inventory.reconcile()

	for _, call := range radio.calls {
		if strings.HasPrefix(call, "OpenWindow") {
			t.Fatalf("the pass opened the window again for a paired request: %v", radio.calls)
		}
	}
}

// arrivingStore is a store whose informer takes one object just after
// the first read of its keys, the way the watch event of a create
// lands while a pass lists.
type arrivingStore struct {
	cache.Store
	arriving *unstructured.Unstructured
}

func (s *arrivingStore) ListKeys() []string {
	keys := s.Store.ListKeys()
	if s.arriving != nil {
		_ = s.Store.Add(s.arriving)
		s.arriving = nil
	}
	return keys
}

// A list from a whole store answers an Adapter the operator created,
// even when the watch event of the create reaches the store during the
// list. The inventory pass reads the list to decide whether its radio's
// Adapter exists, and a list that left it out would make the pass
// create the Adapter again.
func TestAListAnswersACreateThatArrivesDuringTheList(t *testing.T) {
	fixture := newAPIFixture()
	client := testClient(t, fixture.handler(t))
	created := asObject(t, Adapter{Metadata: ObjectMeta{Name: testAdapterName, ResourceVersion: "5"}})
	store := &arrivingStore{Store: cache.NewStore(cache.MetaNamespaceKeyFunc), arriving: created}
	versions := newVersionMemo()
	versions.note(testAdapterName, "5")
	view := storeView{store: store, synced: func() bool { return true }, whole: true}

	list, err := currentList[Adapter](client, heldObjects{view: view, versions: versions}, adapterPath)

	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("the list holds %d Adapters, want 1", len(list))
	}
	if reads := sent(fixture, http.MethodGet); len(reads) != 0 {
		t.Errorf("the list sent %d reads, want none: %v", len(reads), reads)
	}
}
