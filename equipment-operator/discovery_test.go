package main

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/wiim"
)

// mustDeepEqual compares slices and structs, which mustMatch cannot
// because it requires comparable values.
func mustDeepEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the value is %#v, want %#v", got, want)
	}
}

// discoveryAPI serves the Receiver list that a test sets and records
// the names of the Receivers that the operator applied and deleted.
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

// The UUIDs are invented. secondSpell is secondUUID as mDNS spells it:
// sixteen bytes, hyphenated, with the LinkPlay suffix FF98F2F7.
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
	finder := newDiscovery(client, func() {})
	finder.store(found)
	if err := finder.reconcile(); err != nil {
		t.Fatalf("reconciling the discovered Receivers: %v", err)
	}

	// A person's Receiver already names the first amp.
	mustDeepEqual(t, api.appliedNames(), []string{strings.ToLower(secondUUID)})
	mustDeepEqual(t, api.deletedNames(), []string(nil))
}

func TestDiscoveryAddressNormalizesTheIdentity(t *testing.T) {
	t.Parallel()
	finder := newDiscovery(nil, func() {})
	finder.store([]wiim.Device{{UUID: firstUUID, Address: "192.0.2.1"}})

	mustMatch(t, finder.address(firstUUID), "192.0.2.1")
	mustMatch(t, finder.address("ff98f2f7-8136-ce45-a780-d8a1ff98f2f7"), "192.0.2.1")
	mustMatch(t, finder.address(secondUUID), "")
}

func TestDiscoveredNameIsTheLowercasedIdentity(t *testing.T) {
	t.Parallel()
	mustMatch(t, discoveredName("FF98F2F78136CE45A780D8A1"), "ff98f2f78136ce45a780d8a1")
}

// The test replaces the package variable discover, so it cannot run in
// parallel.
func TestDiscoveryRunStopsWithItsContext(t *testing.T) {
	restore := discover
	t.Cleanup(func() { discover = restore })
	discovered := atomic.Int64{}
	discover = func(context.Context, time.Duration) []wiim.Device {
		discovered.Add(1)
		return []wiim.Device{{UUID: secondUUID, Address: "192.0.2.2"}}
	}
	synctest.Test(t, func(t *testing.T) {
		api, client := startDiscoveryAPI(t)
		api.list = receiversWith()
		finder := newDiscovery(client, func() {})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			finder.run(ctx)
			close(done)
		}()

		synctest.Wait()
		mustMatch(t, discovered.Load(), 1)
		time.Sleep(discoveryInterval)
		synctest.Wait()
		mustMatch(t, discovered.Load(), 2)

		cancel()
		<-done
	})
}

// mdnsSpelling returns uuid as mDNS spells it: sixteen bytes,
// hyphenated, with the LinkPlay suffix FF98F2F7.
func mdnsSpelling(uuid string) string {
	return strings.ToLower(uuid[:8]) + "-" + strings.ToLower(uuid[8:12]) + "-" +
		strings.ToLower(uuid[12:16]) + "-" + strings.ToLower(uuid[16:20]) + "-" +
		strings.ToLower(uuid[20:]) + "ff98f2f7"
}

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
			[]string{"discovery found the WiiM " + secondUUID + " at 192.0.2.2, and no Receiver declares it, so discovery created Receiver " + strings.ToLower(secondUUID)},
		},
		{
			"a person's Receiver that names the amp",
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
			"an amp whose Receiver stays",
			[]Receiver{discoveredReceiver(strings.ToLower(firstUUID), firstUUID)},
			[]wiim.Device{{UUID: firstUUID, Address: "192.0.2.1"}},
			[]string{""},
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			api, client := startDiscoveryAPI(t)
			api.list = receiversWith(one.list...)
			finder := newDiscovery(client, func() {})
			finder.now = searchClock()
			log := &logBuffer{}
			finder.log = log
			for range discoveryMisses {
				finder.store(one.found)
			}

			mustSucceed(t, finder.reconcile())

			mustDeepEqual(t, log.lines(), one.want)
		})
	}
}

// searchClock returns a clock that advances 30 s on each call, as
// between the searches of a running operator.
func searchClock() func() time.Time {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return func() time.Time {
		at = at.Add(30 * time.Second)
		return at
	}
}

// Each case reconciles after each search, as a running operator does.
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
		{"an amp that every search finds", [][]wiim.Device{amp, amp, amp}, nil},
		{"an amp missed twice", [][]wiim.Device{amp, nil, nil}, nil},
		{"an amp that is gone", [][]wiim.Device{amp, nil, nil, nil}, []string{strings.ToLower(firstUUID)}},
		{"an amp that returns after a start", [][]wiim.Device{nil, nil, amp, nil, nil}, nil},
		{"an amp that returns before the bound", [][]wiim.Device{amp, nil, nil, amp, nil, nil}, nil},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			api, client := startDiscoveryAPI(t)
			api.list = receiversWith(discoveredReceiver(strings.ToLower(firstUUID), firstUUID))
			finder := newDiscovery(client, func() {})
			finder.now = searchClock()
			finder.log = &logBuffer{}

			mustSucceed(t, finder.reconcile())
			for _, found := range one.searches {
				finder.store(found)
				mustSucceed(t, finder.reconcile())
			}

			mustDeepEqual(t, api.deletedNames(), one.deleted)
			mustDeepEqual(t, api.appliedNames(), []string(nil))
		})
	}
}

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
				wiimReceiver("studio", mdnsSpelling(firstUUID)),
			)
			finder := newDiscovery(client, func() {})
			finder.log = &logBuffer{}
			for _, found := range one.searches {
				finder.store(found)
			}

			mustSucceed(t, finder.reconcile())

			mustDeepEqual(t, api.deletedNames(), []string{strings.ToLower(firstUUID)})
			mustDeepEqual(t, api.appliedNames(), []string(nil))
		})
	}
}

func TestDiscoveryKeepsTheAddressThroughAMissedSearch(t *testing.T) {
	t.Parallel()
	finder := newDiscovery(nil, func() {})
	finder.store([]wiim.Device{{UUID: firstUUID, Address: "192.0.2.1"}})
	finder.store(nil)

	mustMatch(t, finder.address(firstUUID), "192.0.2.1")
}

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
			finder := newDiscovery(client, func() {})
			finder.log = &logBuffer{}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			if one.cut {
				cancel()
			}

			for range discoveryMisses {
				finder.once(ctx)
			}

			mustDeepEqual(t, api.deletedNames(), one.deleted)
		})
	}
}

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
			finder := newDiscovery(client, func() { wakes.Add(1) })
			finder.log = &logBuffer{}

			for range one.searches {
				finder.once(t.Context())
			}

			mustMatch(t, wakes.Load(), one.wakes)
		})
	}
}
