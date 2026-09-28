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
	t.Parallel()
	api, client := startDiscoveryAPI(t)
	api.list = receiversWith(wiimReceiver("studio", firstUUID))

	found := []wiim.Device{
		{UUID: firstUUID, Address: "192.0.2.1"},
		{UUID: secondUUID, Address: "192.0.2.2"},
	}
	held := newDiscovery(client, func() {})
	held.store(found)
	if err := held.reconcile(); err != nil {
		t.Fatal(err)
	}

	// The person's Receiver already names the first amp, so only the
	// second gets an object, named by its identity.
	mustDeepEqual(t, api.appliedNames(), []string{strings.ToLower(secondUUID)})
	mustDeepEqual(t, api.deletedNames(), []string(nil))
}

func TestDiscoveryAddressNormalizesTheIdentity(t *testing.T) {
	t.Parallel()
	held := newDiscovery(nil, func() {})
	held.store([]wiim.Device{{UUID: firstUUID, Address: "192.0.2.1"}})

	mustMatch(t, held.address(firstUUID), "192.0.2.1")
	mustMatch(t, held.address("ff98f2f7-8136-ce45-a780-d8a1ff98f2f7"), "192.0.2.1")
	mustMatch(t, held.address(secondUUID), "")
}

func TestDiscoveredNameIsTheLowercasedIdentity(t *testing.T) {
	t.Parallel()
	mustMatch(t, discoveredName("FF98F2F78136CE45A780D8A1"), "ff98f2f78136ce45a780d8a1")
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

// Discovery writes one line for each Receiver it creates or deletes,
// and none for an amp whose Receiver stands.
func TestDiscoveryLogsEachReceiverItCreatesOrDeletes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		list  []Receiver
		found []wiim.Device
		want  []string
	}{
		{
			"an amp no Receiver names",
			nil,
			[]wiim.Device{{UUID: secondUUID, Address: "192.0.2.2"}},
			[]string{"discovery found the WiiM " + secondUUID + " at 192.0.2.2, which no Receiver names; created Receiver " + strings.ToLower(secondUUID)},
		},
		{
			"a person's Receiver that takes over",
			[]Receiver{discoveredReceiver(strings.ToLower(firstUUID), firstUUID), wiimReceiver("studio", firstUUID)},
			[]wiim.Device{{UUID: firstUUID, Address: "192.0.2.1"}},
			[]string{"deleted the discovered Receiver " + strings.ToLower(firstUUID) + ": Receiver studio names the WiiM " + firstUUID},
		},
		{
			"an amp that is gone",
			[]Receiver{discoveredReceiver(strings.ToLower(secondUUID), secondUUID)},
			nil,
			[]string{"deleted the discovered Receiver " + strings.ToLower(secondUUID) + ": the WiiM " + secondUUID + " missed 3 searches in a row over 60 s"},
		},
		{
			"an amp whose Receiver stands",
			[]Receiver{discoveredReceiver(strings.ToLower(firstUUID), firstUUID)},
			[]wiim.Device{{UUID: firstUUID, Address: "192.0.2.1"}},
			[]string{""},
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			api, client := startDiscoveryAPI(t)
			api.list = receiversWith(one.list...)
			held := newDiscovery(client, func() {})
			held.now = searchClock()
			log := &logBuffer{}
			held.log = log
			for range discoveryMisses {
				held.store(one.found)
			}

			mustSucceed(t, held.reconcile())

			mustDeepEqual(t, log.lines(), one.want)
		})
	}
}

// searchClock answers a time 30 s later on each call, as the searches
// of a running operator are.
func searchClock() func() time.Time {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return func() time.Time {
		at = at.Add(30 * time.Second)
		return at
	}
}

// Each case runs the full searches in order and reconciles after each
// one, as a running operator does. The operator's own Receiver for the
// amp stands until discoveryMisses searches in a row miss the amp. The
// count starts with the operator, so a start that has not heard from the
// amp yet is no evidence either way.
func TestDiscoveryDeletesItsOwnOnlyAfterTheAmpMissesEnoughSearches(t *testing.T) {
	t.Parallel()
	amp := []wiim.Device{{UUID: firstUUID, Address: "192.0.2.1"}}
	cases := []struct {
		name     string
		searches [][]wiim.Device
		deleted  []string
	}{
		{"a start with no search yet", nil, nil},
		{"a start whose first search misses", [][]wiim.Device{nil}, nil},
		{"a start whose first two searches miss", [][]wiim.Device{nil, nil}, nil},
		{"a start whose searches never find the amp", [][]wiim.Device{nil, nil, nil}, []string{strings.ToLower(firstUUID)}},
		{"an amp that stands", [][]wiim.Device{amp, amp, amp}, nil},
		{"an amp missed twice", [][]wiim.Device{amp, nil, nil}, nil},
		{"an amp that is gone", [][]wiim.Device{amp, nil, nil, nil}, []string{strings.ToLower(firstUUID)}},
		{"an amp that returns after a start", [][]wiim.Device{nil, nil, amp, nil, nil}, nil},
		{"an amp that returns before the bound", [][]wiim.Device{amp, nil, nil, amp, nil, nil}, nil},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			api, client := startDiscoveryAPI(t)
			api.list = receiversWith(discoveredReceiver(strings.ToLower(firstUUID), firstUUID))
			held := newDiscovery(client, func() {})
			held.now = searchClock()
			held.log = &logBuffer{}

			mustSucceed(t, held.reconcile())
			for _, found := range one.searches {
				held.store(found)
				mustSucceed(t, held.reconcile())
			}

			mustDeepEqual(t, api.deletedNames(), one.deleted)
			mustDeepEqual(t, api.appliedNames(), []string(nil))
		})
	}
}

// A person's Receiver that names the amp replaces the operator's copy at
// once, whatever the searches have found.
func TestDiscoveryPrunesItsOwnWhenAPersonTakesOver(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		searches [][]wiim.Device
	}{
		{"before any search", nil},
		{"after a search that missed the amp", [][]wiim.Device{nil}},
		{"after a search that found the amp", [][]wiim.Device{{{UUID: firstUUID, Address: "192.0.2.1"}}}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			api, client := startDiscoveryAPI(t)
			api.list = receiversWith(
				discoveredReceiver(strings.ToLower(firstUUID), firstUUID),
				wiimReceiver("studio", firstSpellOf(firstUUID)),
			)
			held := newDiscovery(client, func() {})
			held.log = &logBuffer{}
			for _, found := range one.searches {
				held.store(found)
			}

			mustSucceed(t, held.reconcile())

			mustDeepEqual(t, api.deletedNames(), []string{strings.ToLower(firstUUID)})
			mustDeepEqual(t, api.appliedNames(), []string(nil))
		})
	}
}

// One missed search is multicast, not an amp that left, so the address
// stands through it.
func TestDiscoveryKeepsTheAddressThroughAMissedSearch(t *testing.T) {
	t.Parallel()
	held := newDiscovery(nil, func() {})
	held.store([]wiim.Device{{UUID: firstUUID, Address: "192.0.2.1"}})
	held.store(nil)

	mustMatch(t, held.address(firstUUID), "192.0.2.1")
}

// A search its context cut short did not run its full window, so it
// does not count as a miss.
func TestDiscoveryCountsOnlyFullSearches(t *testing.T) {
	cases := []struct {
		name    string
		cut     bool
		deleted []string
	}{
		{"full searches", false, []string{strings.ToLower(firstUUID)}},
		{"searches cut short", true, nil},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			api, client := startDiscoveryAPI(t)
			api.list = receiversWith(discoveredReceiver(strings.ToLower(firstUUID), firstUUID))
			restore := discover
			t.Cleanup(func() { discover = restore })
			discover = func(context.Context, time.Duration) []wiim.Device { return nil }
			held := newDiscovery(client, func() {})
			held.log = &logBuffer{}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			if one.cut {
				cancel()
			}

			for range discoveryMisses {
				held.once(ctx)
			}

			mustDeepEqual(t, api.deletedNames(), one.deleted)
		})
	}
}

// The Receiver loop reads only the addresses from discovery, so a
// search wakes it only when an address appeared, moved, or went.
func TestDiscoveryWakesTheLoopOnlyWhenAnAddressMoves(t *testing.T) {
	here := wiim.Device{UUID: firstUUID, Address: "192.0.2.1"}
	there := wiim.Device{UUID: firstUUID, Address: "192.0.2.2"}
	cases := []struct {
		name     string
		searches [][]wiim.Device
		wakes    int64
	}{
		{"a new amp", [][]wiim.Device{{here}}, 1},
		{"the same amp at the same address", [][]wiim.Device{{here}, {here}, {here}}, 1},
		{"an amp at a new address", [][]wiim.Device{{here}, {there}}, 2},
		{"an amp that one search missed", [][]wiim.Device{{here}, nil}, 1},
		{"an amp that enough searches missed", [][]wiim.Device{{here}, nil, nil, nil}, 2},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			api, client := startDiscoveryAPI(t)
			api.list = receiversWith(wiimReceiver("den", firstUUID))
			searches := one.searches
			restore := discover
			t.Cleanup(func() { discover = restore })
			discover = func(context.Context, time.Duration) []wiim.Device {
				found := searches[0]
				searches = searches[1:]
				return found
			}
			var wakes atomic.Int64
			held := newDiscovery(client, func() { wakes.Add(1) })
			held.log = &logBuffer{}

			for range one.searches {
				held.once(t.Context())
			}

			mustMatch(t, wakes.Load(), one.wakes)
		})
	}
}
