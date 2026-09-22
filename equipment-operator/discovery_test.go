package main

// The discovery loop against a fake API server and a fixed found set.

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/wiim"
)

// mustDeepEqual is the slice and struct comparison the generic
// mustMatch cannot express.
func mustDeepEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

// discoveryAPI answers the collection a test sets and records the
// Receivers the operator applied and deleted.
type discoveryAPI struct {
	mutex   sync.Mutex
	list    ReceiverList
	applied []string
	deleted []string
}

func startDiscoveryAPI(t *testing.T) (*discoveryAPI, *Client) {
	t.Helper()
	api := &discoveryAPI{}
	return api, testAPIClient(t, http.HandlerFunc(api.handle))
}

func (a *discoveryAPI) handle(w http.ResponseWriter, r *http.Request) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	name := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, receiversPath), "/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == receiversPath:
		_ = json.NewEncoder(w).Encode(a.list)
	case r.Method == http.MethodPatch:
		a.applied = append(a.applied, name)
		_ = json.NewEncoder(w).Encode(&Receiver{Metadata: ObjectMeta{Name: name}})
	case r.Method == http.MethodDelete:
		a.deleted = append(a.deleted, name)
		_ = json.NewEncoder(w).Encode(&Receiver{Metadata: ObjectMeta{Name: name}})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (a *discoveryAPI) appliedNames() []string {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return append([]string(nil), a.applied...)
}

func (a *discoveryAPI) deletedNames() []string {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return append([]string(nil), a.deleted...)
}

// The two identities the tests use. They are invented, and the second
// is spelled the way mDNS spells a sixteen-byte UUID.
const (
	firstUUID   = "FF98F2F78136CE45A780D8A1"
	secondUUID  = "FF98F2F7702D05D4ADE453C4"
	secondSpell = "ff98f2f7-702d-05d4-ade4-53c4ff98f2f7"
)

func receiversWith(items ...Receiver) ReceiverList {
	return ReceiverList{Items: items}
}

func wiimReceiver(name, uuid string) Receiver {
	return Receiver{Metadata: ObjectMeta{Name: name}, Spec: ReceiverSpec{Wiim: &WiimProtocol{UUID: uuid}}}
}

func discoveredReceiver(name, uuid string) Receiver {
	receiver := wiimReceiver(name, uuid)
	receiver.Metadata.Labels = map[string]string{discoveredLabel: "wiim"}
	return receiver
}

func TestDiscoveryCreatesUnclaimedAndDefersToAPerson(t *testing.T) {
	api, client := startDiscoveryAPI(t)
	api.list = receiversWith(wiimReceiver("studio-wiim", firstUUID))

	found := []wiim.Device{
		{UUID: firstUUID, Address: "192.0.2.1"},
		{UUID: secondUUID, Address: "192.0.2.2"},
	}
	held := newDiscovery(client, func() {})
	if err := held.reconcile(found); err != nil {
		t.Fatal(err)
	}

	// The person's Receiver already names the first amp, so only the
	// second gets an object, named by its identity.
	mustDeepEqual(t, api.appliedNames(), []string{strings.ToLower(secondUUID)})
	mustDeepEqual(t, api.deletedNames(), []string(nil))
}

func TestDiscoveryPrunesItsOwnWhenAPersonTakesOver(t *testing.T) {
	api, client := startDiscoveryAPI(t)
	api.list = receiversWith(
		discoveredReceiver(strings.ToLower(firstUUID), firstUUID),
		wiimReceiver("studio-wiim", firstSpellOf(firstUUID)),
	)

	held := newDiscovery(client, func() {})
	if err := held.reconcile([]wiim.Device{{UUID: firstUUID, Address: "192.0.2.1"}}); err != nil {
		t.Fatal(err)
	}

	mustDeepEqual(t, api.deletedNames(), []string{strings.ToLower(firstUUID)})
	mustDeepEqual(t, api.appliedNames(), []string(nil))
}

func TestDiscoveryPrunesItsOwnWhenTheAmpIsGone(t *testing.T) {
	api, client := startDiscoveryAPI(t)
	api.list = receiversWith(discoveredReceiver(strings.ToLower(secondUUID), secondUUID))

	held := newDiscovery(client, func() {})
	if err := held.reconcile(nil); err != nil {
		t.Fatal(err)
	}

	mustDeepEqual(t, api.deletedNames(), []string{strings.ToLower(secondUUID)})
}

func TestDiscoveryKeepsItsOwnWhileTheAmpStands(t *testing.T) {
	api, client := startDiscoveryAPI(t)
	api.list = receiversWith(discoveredReceiver(strings.ToLower(firstUUID), firstUUID))

	held := newDiscovery(client, func() {})
	if err := held.reconcile([]wiim.Device{{UUID: firstUUID, Address: "192.0.2.1"}}); err != nil {
		t.Fatal(err)
	}

	mustDeepEqual(t, api.deletedNames(), []string(nil))
	mustDeepEqual(t, api.appliedNames(), []string(nil))
}

func TestDiscoveryAddressNormalizesTheIdentity(t *testing.T) {
	held := newDiscovery(nil, func() {})
	held.store([]wiim.Device{{UUID: firstUUID, Address: "192.0.2.1"}})

	mustMatch(t, held.address(firstUUID), "192.0.2.1")
	mustMatch(t, held.address("ff98f2f7-8136-ce45-a780-d8a1ff98f2f7"), "192.0.2.1")
	mustMatch(t, held.address(secondUUID), "")
}

func TestDiscoveredNameIsTheLowercasedIdentity(t *testing.T) {
	mustMatch(t, discoveredName("FF98F2F78136CE45A780D8A1"), "ff98f2f78136ce45a780d8a1")
}

func TestClaimedByOther(t *testing.T) {
	mustMatch(t, claimedByOther([]string{"only"}, "only"), false)
	mustMatch(t, claimedByOther([]string{"mine", "theirs"}, "mine"), true)
	mustMatch(t, claimedByOther(nil, "mine"), false)
}

// run searches, reconciles, and stops with its context.
func TestDiscoveryRunStopsWithItsContext(t *testing.T) {
	api, client := startDiscoveryAPI(t)
	api.list = receiversWith()

	restoreDiscover := discover
	restoreInterval := discoveryInterval
	t.Cleanup(func() { discover, discoveryInterval = restoreDiscover, restoreInterval })
	discovered := atomic.Int64{}
	discover = func(context.Context, time.Duration) []wiim.Device {
		discovered.Add(1)
		return []wiim.Device{{UUID: secondUUID, Address: "192.0.2.2"}}
	}
	discoveryInterval = 2 * time.Millisecond

	held := newDiscovery(client, func() {})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		held.run(ctx)
		close(done)
	}()
	deadline := time.After(2 * time.Second)
	for discovered.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("discovery never ran")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("discovery did not stop with its context")
	}
}

// firstSpellOf spells one identity the way mDNS does, so a test proves
// the two spellings compare equal.
func firstSpellOf(uuid string) string {
	return strings.ToLower(uuid[:8]) + "-" + strings.ToLower(uuid[8:12]) + "-" +
		strings.ToLower(uuid[12:16]) + "-" + strings.ToLower(uuid[16:20]) + "-" +
		strings.ToLower(uuid[20:]) + "ff98f2f7"
}
