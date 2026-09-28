package main

// These tests count the requests a pass sends when it reads this
// machine's Sinks and Sources from the watches' stores, and cover a
// status write from a copy the store holds that is older than the API
// server's.

import (
	"context"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/tools/cache"
)

// cacheOf is a cache that holds what the fixture holds now, the way the
// two stores hold it once the watches have delivered every write.
func cacheOf(t *testing.T, api *endpointAPI) endpointCache {
	t.Helper()
	sinks, sources := cache.NewStore(cache.MetaNamespaceKeyFunc), cache.NewStore(cache.MetaNamespaceKeyFunc)
	api.mutex.Lock()
	defer api.mutex.Unlock()
	for _, sink := range api.sinks {
		if err := sinks.Add(asObject(t, *sink)); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range api.sources {
		if err := sources.Add(asObject(t, *source)); err != nil {
			t.Fatal(err)
		}
	}
	return endpointCache{sinks: sinks, sources: sources, synced: func() bool { return true }}
}

// reads counts the GET requests among the requests the fixture
// received.
func reads(api *endpointAPI) int {
	api.mutex.Lock()
	defer api.mutex.Unlock()
	count := 0
	for _, request := range api.requests {
		if strings.HasPrefix(request, "GET ") {
			count++
		}
	}
	return count
}

// A settled pass over the lab card's two endpoints and a speaker sends
// one GET for each endpoint when it reads the API server, and none
// when it reads the stores. A pass that finds a new set of endpoints
// also sweeps, which lists both collections from the API server and
// from nothing when the stores hold their first read.
func TestAPassFromTheStoresSendsNoRead(t *testing.T) {
	for _, c := range []struct {
		name   string
		cached bool
		sweep  bool
		want   int
	}{
		{"the API server, a settled pass", false, false, 3},
		{"the stores, a settled pass", true, false, 0},
		{"the API server, a pass that sweeps", false, true, 4},
		{"the stores, a pass that sweeps", true, true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := newEndpointAPI()
			control := testEndpointControl(t, api, &writeRecord{})
			ctx := context.Background()
			if err := control.pass(ctx, labEndpoints(), testSpeakers(), labGraph()); err != nil {
				t.Fatal(err)
			}
			if c.cached {
				control.cache = cacheOf(t, api)
			}
			speakers := testSpeakers()
			if c.sweep {
				// The speaker left, so the endpoints differ from the last
				// sweep's.
				speakers = nil
			}
			api.mutex.Lock()
			api.requests = nil
			api.mutex.Unlock()

			if err := control.pass(ctx, labEndpoints(), speakers, labGraph()); err != nil {
				t.Fatal(err)
			}
			if got := reads(api); got != c.want {
				t.Errorf("the pass sent %d reads, want %d: %v", got, c.want, api.requests)
			}
		})
	}
}

// A store's copy can be older than the operator's own last status
// write. The write from it is refused with a conflict, and the pass
// reads the resource from the API server and writes once more, so the
// status lands.
func TestAStatusWriteFromAnOlderCopyReadsAgainAndLands(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	ctx := context.Background()
	if err := control.pass(ctx, labEndpoints(), nil, labGraph()); err != nil {
		t.Fatal(err)
	}
	control.cache = cacheOf(t, api)
	// The fixture takes a write the store has not seen, so every copy in
	// the store carries an older version.
	api.mutex.Lock()
	for _, sink := range api.sinks {
		sink.Metadata.ResourceVersion = api.nextVersion()
	}
	for _, source := range api.sources {
		source.Metadata.ResourceVersion = api.nextVersion()
	}
	api.mutex.Unlock()
	control.now = func() time.Time { return factsTime.Add(time.Hour) }

	// The capture endpoint is gone, so the sweep writes its Source's
	// absence from the older copy.
	if err := control.pass(ctx, labEndpoints()[:1], nil, labGraph()); err != nil {
		t.Fatalf("the pass failed: %v", err)
	}
	for _, held := range api.sources[testSourceName].Status.Conditions {
		if held.Type == ConnectedCondition && held.Status != conditionFalse {
			t.Errorf("the Source reports %s %s, want the absence reported", held.Type, held.Status)
		}
	}
	if !strings.Contains(strings.Join(api.requests, " "), "GET "+SourcesPath+"/"+testSourceName) {
		t.Errorf("the pass did not read the Source again after the conflict: %v", api.requests)
	}
}

// staleCache runs a first pass, fills the stores from the fixture, and
// then moves every resource in the fixture to a newer version, so every
// copy in the stores is older than the API server's.
func staleCache(t *testing.T, api *endpointAPI, control *endpointControl) {
	t.Helper()
	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph()); err != nil {
		t.Fatal(err)
	}
	control.cache = cacheOf(t, api)
	api.mutex.Lock()
	defer api.mutex.Unlock()
	for _, sink := range api.sinks {
		sink.Metadata.ResourceVersion = api.nextVersion()
	}
	for _, source := range api.sources {
		source.Metadata.ResourceVersion = api.nextVersion()
	}
}

// A claim on the analog sink changes its status. The write from the
// store's older copy is refused, and the pass reads the Sink again and
// writes the claim.
func TestAReconcileFromAnOlderCopyReadsAgainAndLands(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	staleCache(t, api, control)
	control.claims.prepared("claim-1", EndpointClaim{Namespace: "media", Name: "den"}, []string{testAnalogName})

	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph()); err != nil {
		t.Fatalf("the pass failed: %v", err)
	}
	if claim := api.sinks[testAnalogName].Status.Claim; claim == nil || claim.Name != "den" {
		t.Errorf("the analog sink's claim is %+v, want den", claim)
	}
}

// A Sink that another machine took since the store's copy, such as a
// Bluetooth speaker that moved, is left alone by this machine's sweep,
// even after the conflict makes the sweep read it again.
func TestTheSweepLeavesASinkAnotherMachineTook(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	staleCache(t, api, control)
	api.mutex.Lock()
	api.sinks[testAnalogName].Status.Node = "stick-1"
	taken := api.sinks[testAnalogName].Status
	api.mutex.Unlock()

	// The analog endpoint is gone from this machine, so the sweep finds
	// its Sink in the store under this machine's name.
	if err := control.pass(context.Background(), labEndpoints()[1:], nil, labGraph()); err != nil {
		t.Fatalf("the pass failed: %v", err)
	}
	if got := api.sinks[testAnalogName].Status; got.NodeName != taken.NodeName || got.Node != "stick-1" {
		t.Errorf("the other machine's Sink was swept: %+v", got)
	}
}

// A Sink somebody deleted after the store took its copy is created
// again when the pass has a status to write. A pass with nothing to
// write sends nothing, and the delete's own event takes the copy out of
// the store and wakes the pass that creates the Sink.
func TestASinkDeletedSinceTheStoresCopyIsCreatedAgain(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	staleCache(t, api, control)
	api.mutex.Lock()
	delete(api.sinks, testAnalogName)
	api.mutex.Unlock()
	control.claims.prepared("claim-1", EndpointClaim{Namespace: "media", Name: "den"}, []string{testAnalogName})

	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph()); err != nil {
		t.Fatalf("the pass failed: %v", err)
	}
	if sink, held := api.sinks[testAnalogName]; !held || sink.Status.Node != "liken-1" {
		t.Errorf("the analog sink is %+v, %v; want it created again with its status", sink, held)
	}
}
