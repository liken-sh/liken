package main

// A store's copy can be older than this operator's own last write. These
// tests freeze each store at a snapshot taken before a pass wrote, as a
// watch that has not delivered the writes yet would leave it, and check
// that the next pass does not repeat what it already did.

import (
	"encoding/json"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/informer"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// snapshot returns a synced store with the current contents of one
// collection of the fake API server, including each object's
// resourceVersion. Writes to the fake after this call leave the store
// older than the API server.
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

// While the stores lag behind the writes, each pass reads the objects it
// wrote from the API server. The first pass creates the discovered
// Television. The second pass writes that Television's derived status,
// because it reads the Television from the API server though the store
// does not have it yet. The third pass writes no derived status again
// for the CECBus or the Television, and does not create the Television
// again.
func TestAPassDoesNotActOnACopyOlderThanItsOwnWrite(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.putBus(scannedBus("den", tvDevice, receiverDevice))
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putReceiver(wiredReceiver("den", "node-1", "acm-0001-receiver"))
		controller := newCECBusController(api.client, testNamespace)
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
			`CECBus den reports a TV at 0.0.0.0 named "TV", and no Television names the bus, so the operator created Television den`,
		})
	})
}

// A Receiver unit's status write can reach the API server before the
// watch delivers it. A new unit starts from the settled state in the
// stored status, and a unit that read the store's older copy would send
// the receiver a setting again. So a read after the write must return
// the API server's copy.
func TestAReceiverReadAfterItsOwnStatusWriteReturnsTheWrite(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.putReceiver(receiverAt("uid-1", 1, "10.0.0.8"))
		held := snapshot(t, api, receiversPath)
		_, err := ApplyReceiverStatus(api.client, "theater", ReceiverStatus{Address: "10.0.0.9"})
		mustSucceed(t, err)

		list, err := readReceivers(api.client, held)

		mustSucceed(t, err)
		mustMatch(t, len(list.Items), 1)
		mustMatch(t, list.Items[0].Status.Address, "10.0.0.9")
	})
}

// When the store's copy of a Television is older than the operator's own
// write, the pass reads it from the API server. While the API server
// refuses that read, the read returns an error, so the pass changes
// nothing and tries again. Once the API server responds, the read
// returns the written copy.
func TestARefusedReadOfACopyOlderThanItsOwnWriteIsAnError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
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
	})
}

// A pass that runs before the Television watch finishes its first list
// reads the Televisions from the API server. The store can then become
// ready with an older copy, because the watch's first list can come from
// the API server's watch cache, which can lag behind etcd. The next
// pass must not read that older copy. Otherwise a node workload that
// read a session's wake from the API server and started it would then
// read the copy from before the wake, and cancel the wake it just
// started.
func TestAStoreThatBecomesReadyWithAnOlderCopyIsNotReadAfterAList(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.putTelevision(lounge(""))
		older := snapshot(t, api, televisionsPath).view()
		session := wokeNow()
		api.putTelevision(waking(session))
		held := &watchStore{}
		held.hold(informer.View{Store: older.Store, Synced: func() bool { return false }}, false)

		listed, err := readTelevisions(api.client, held)
		mustSucceed(t, err)
		mustDeepEqual(t, listed.Items[0].Status.Session, session)
		held.hold(older, false)
		read, err := readTelevisions(api.client, held)

		mustSucceed(t, err)
		mustDeepEqual(t, read.Items[0].Status.Session, session)
	})
}
