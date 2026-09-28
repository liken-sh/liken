package main

// A store's copy can be older than this operator's own last write. These
// tests hold each store at a snapshot taken before a pass wrote, the way
// a watch that has not delivered the writes yet holds it, and prove the
// next pass does not act again on what it already did.

import (
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"
)

// snapshot is a synced store that holds one collection of the fake as
// it is now, with each object's resourceVersion. Later writes to the
// fake leave the store older than the API server.
func snapshot(t *testing.T, api *cecAPI, path string) *watchStore {
	t.Helper()
	api.mutex.Lock()
	objects := api.collection(path)
	api.mutex.Unlock()
	var held []*unstructured.Unstructured
	for _, name := range sortedKeys(objects) {
		encoded, err := json.Marshal(objects[name])
		mustSucceed(t, err)
		item := &unstructured.Unstructured{}
		mustSucceed(t, item.UnmarshalJSON(encoded))
		held = append(held, item)
	}
	return heldStore(t, true, held...)
}

// A pass that runs before the watches delivered the earlier passes'
// writes reads each object it wrote from the API server. The first pass
// creates the discovered Television, and the second writes its derived
// status, because the second reads the Television it created though the
// store does not hold it. The third writes the derived status of no
// CECBus and no Television again, and creates no Television again.
func TestAPassDoesNotActOnACopyOlderThanItsOwnWrite(t *testing.T) {
	api := startCECAPI(t)
	api.putBus(scannedBus("den", tvDevice, receiverDevice))
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putReceiver(wiredReceiver("den", "node-1", "acm-0001-receiver"))
	controller := newCECBusController(api.client)
	log := &logBuffer{}
	controller.log = log
	controller.buses = snapshot(t, api, cecBusesPath)
	controller.televisions = snapshot(t, api, televisionsPath)
	controller.displays = snapshot(t, api, displaysPath)
	controller.receivers = snapshot(t, api, receiversPath)
	mustSucceed(t, controller.pass())
	mustSucceed(t, controller.pass())
	busWrites, televisionWrites := api.busWrites, api.derivedWrites

	mustSucceed(t, controller.pass())

	mustMatch(t, api.busWrites, busWrites)
	mustMatch(t, api.derivedWrites, televisionWrites)
	mustDeepEqual(t, api.created, []string{"den"})
	mustDeepEqual(t, log.lines(), []string{
		`CECBus den reports a TV at 0.0.0.0 named "TV", and no Television names the bus; created Television den`,
	})
}

// A Receiver unit's status write can reach the API server before the
// watch delivers it. A new unit takes what the old one settled from the
// stored status, and one that read the store's older copy would send
// the receiver a setting again. The read after the write answers the
// API server's copy.
func TestAReceiverReadAfterItsOwnStatusWriteAnswersTheWrite(t *testing.T) {
	api := startCECAPI(t)
	api.putReceiver(receiverAt("uid-1", 1, "10.0.0.8"))
	held := snapshot(t, api, receiversPath)
	_, err := ApplyReceiverStatus(api.client, "theater", ReceiverStatus{Address: "10.0.0.9"})
	mustSucceed(t, err)

	list, err := readReceivers(api.client, held)

	mustSucceed(t, err)
	mustMatch(t, len(list.Items), 1)
	mustMatch(t, list.Items[0].Status.Address, "10.0.0.9")
}

// A pass reads a Television from the API server when the store's copy
// is older than the operator's own write. While the API server refuses
// that read, the read is an error, and the pass acts on nothing and
// tries again. Once the API server answers, the read answers the write.
func TestARefusedReadOfACopyOlderThanItsOwnWriteIsAnError(t *testing.T) {
	api := startCECAPI(t)
	api.putTelevision(Television{Metadata: ObjectMeta{Name: "lounge"}, Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: "den"}}})
	held := snapshot(t, api, televisionsPath)
	mustSucceed(t, ApplyTelevisionSession(api.client, "lounge", &TelevisionSession{Player: "den"}))
	api.refuseTelevisions(true)

	_, refused := readTelevisions(api.client, held)
	api.refuseTelevisions(false)
	list, answered := readTelevisions(api.client, held)

	mustMatch(t, refused != nil, true)
	mustSucceed(t, answered)
	mustMatch(t, list.Items[0].Status.Session.Player, "den")
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

// A list from a whole store answers an object the operator created,
// even when the watch event of the create reaches the store during the
// list. The node workload's pass reads the list to decide whether its
// discovered CECBus exists, and a list that left it out would make the
// pass create the CECBus again.
func TestAListAnswersACreateThatArrivesDuringTheList(t *testing.T) {
	api := startCECAPI(t)
	created := asObject(t, CECBus{Metadata: ObjectMeta{Name: "node-1", ResourceVersion: "5"}})
	store := &arrivingStore{Store: cache.NewStore(cache.MetaNamespaceKeyFunc), arriving: created}
	versions := newVersionMemo()
	versions.note("node-1", "5")
	view := storeView{store: store, synced: func() bool { return true }, whole: true}

	list, err := currentList[CECBus](api.client, heldObjects{view: view, versions: versions}, cecBusPath)

	mustSucceed(t, err)
	mustMatch(t, len(list), 1)
	mustMatch(t, api.reads[cecBusPath("node-1")], 0)
}
