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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// cacheOf is a cache that holds what the fixture holds now, the way the
// two stores hold it once the watches have delivered every write.
func cacheOf(t *testing.T, api *endpointAPI) objectCache {
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
	synced := func() bool { return true }
	return objectCache{
		sinks:   informer.Held{View: informer.View{Store: sinks, Synced: synced}},
		sources: informer.Held{View: informer.View{Store: sources, Synced: synced}},
	}
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
// from nothing when the stores hold their first read. The memo of the
// first pass's writes costs no read once the stores hold those writes.
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
			withMemos(control)
			ctx := context.Background()
			if err := control.pass(ctx, labEndpoints(), testSpeakers(), labGraph(), nil); err != nil {
				t.Fatal(err)
			}
			if c.cached {
				delivered(t, api, control)
			}
			speakers := testSpeakers()
			if c.sweep {
				// The speaker left, so the endpoints differ from the last
				// sweep's.
				speakers = nil
			}
			forget(api)

			if err := control.pass(ctx, labEndpoints(), speakers, labGraph(), nil); err != nil {
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
	if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
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
	if err := control.pass(ctx, labEndpoints()[:1], nil, labGraph(), nil); err != nil {
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

// withMemos gives the pass the memos it runs with in a pod, and no
// store.
func withMemos(control *endpointControl) {
	control.cache = objectCache{
		sinks:   informer.Held{Versions: memo.New()},
		sources: informer.Held{Versions: memo.New()},
	}
}

// delivered fills the stores from the fixture, the way the watches
// hold every write once they deliver it, and keeps the pass's memos.
func delivered(t *testing.T, api *endpointAPI, control *endpointControl) {
	t.Helper()
	memos := control.cache
	control.cache = cacheOf(t, api)
	control.cache.sinks.Versions = memos.sinks.Versions
	control.cache.sources.Versions = memos.sources.Versions
}

// forget clears the requests the fixture received.
func forget(api *endpointAPI) {
	api.mutex.Lock()
	defer api.mutex.Unlock()
	api.requests = nil
}

// staleCache runs a first pass with the memos, fills the stores from
// the fixture, and then moves every resource in the fixture to a newer
// version, the way another writer's change does, so every copy in the
// stores is older than the API server's and the memo does not know it.
func staleCache(t *testing.T, api *endpointAPI, control *endpointControl) {
	t.Helper()
	withMemos(control)
	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatal(err)
	}
	delivered(t, api, control)
	api.mutex.Lock()
	defer api.mutex.Unlock()
	for _, sink := range api.sinks {
		sink.Metadata.ResourceVersion = api.nextVersion()
	}
	for _, source := range api.sources {
		source.Metadata.ResourceVersion = api.nextVersion()
	}
}

// A list before the Sink watch finished its first read comes from the
// API server. The store can then become ready at an older copy, because
// the watch's first read can come from the API server's watch cache.
// The next list must not answer that older copy: a pass that read a
// claim on the Sink from the first list would read the copy from before
// the claim, and act as if the Sink were free.
func TestAListBeforeTheStoreIsReadyIsNeverReadOlder(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	api.sinks[testSinkName] = &Sink{Metadata: EndpointMeta{Name: testSinkName, ResourceVersion: api.nextVersion()},
		Status: sinkStatus(EndpointStatus{Node: "liken-1"})}
	older := cacheOf(t, api).sinks.View
	api.sinks[testSinkName].Status.Claim = &EndpointClaim{Namespace: "media", Name: "den"}
	api.sinks[testSinkName].Metadata.ResourceVersion = api.nextVersion()
	ready := false
	control.cache.sinks = informer.Held{View: informer.View{Store: older.Store, Synced: func() bool { return ready }}, Versions: memo.New()}

	listed, err := control.readSinks()
	if err != nil || len(listed) != 1 || listed[0].Status.Claim == nil {
		t.Fatalf("the list before the store is ready = %+v, %v; want the claim", listed, err)
	}
	ready = true
	read, err := control.readSinks()

	if err != nil || len(read) != 1 || read[0].Status.Claim == nil {
		t.Errorf("the list from the ready store = %+v, %v; want the claim, not the store's older copy", read, err)
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

	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph(), nil); err != nil {
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
	api.sinks[testAnalogName].Status.Node = "node-2"
	taken := api.sinks[testAnalogName].Status
	api.mutex.Unlock()

	// The analog endpoint is gone from this machine, so the sweep finds
	// its Sink in the store under this machine's name.
	if err := control.pass(context.Background(), labEndpoints()[1:], nil, labGraph(), nil); err != nil {
		t.Fatalf("the pass failed: %v", err)
	}
	if got := api.sinks[testAnalogName].Status; got.NodeName != taken.NodeName || got.Node != "node-2" {
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

	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("the pass failed: %v", err)
	}
	if sink, held := api.sinks[testAnalogName]; !held || sink.Status.Node != "liken-1" {
		t.Errorf("the analog sink is %+v, %v; want it created again with its status", sink, held)
	}
}

// The store can still hold a Sink as it was before this operator wrote
// a claim into its status. When the claim is released, the status the
// pass composes matches that older copy. The operator remembers the
// version its own write produced, reads the Sink from the API server
// instead of the store's older copy, and writes the release, so the
// status does not keep a claim that is gone.
func TestAPassDoesNotActOnACopyOlderThanItsOwnWrite(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	withMemos(control)
	ctx := context.Background()
	if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatal(err)
	}
	delivered(t, api, control)
	control.claims.prepared("claim-1", EndpointClaim{Namespace: "media", Name: "den"}, []string{testAnalogName})
	if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatal(err)
	}
	if claim := api.sinks[testAnalogName].Status.Claim; claim == nil {
		t.Fatal("the pass did not write the claim")
	}

	control.claims.released("claim-1")
	forget(api)
	if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatal(err)
	}

	if got := reads(api); got != 1 {
		t.Errorf("the pass sent %d reads, want 1, of the Sink it wrote: %v", got, api.requests)
	}
	if claim := api.sinks[testAnalogName].Status.Claim; claim != nil {
		t.Errorf("the Sink still reports the released claim %+v", claim)
	}
}

// sent counts the requests of one method the fixture received.
func sent(api *endpointAPI, method string) int {
	api.mutex.Lock()
	defer api.mutex.Unlock()
	count := 0
	for _, request := range api.requests {
		if strings.HasPrefix(request, method+" ") {
			count++
		}
	}
	return count
}

// ownWriteUndelivered runs a pass that creates the lab card's
// resources, fills the stores, and runs a pass that writes a claim onto
// the analog Sink, so the Sink store's copy is older than the
// operator's own write.
func ownWriteUndelivered(t *testing.T) (*endpointAPI, *endpointControl) {
	t.Helper()
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	withMemos(control)
	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatal(err)
	}
	delivered(t, api, control)
	control.claims.prepared("claim-1", EndpointClaim{Namespace: "media", Name: "den"}, []string{testAnalogName})
	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatal(err)
	}
	forget(api)
	return api, control
}

// The sweep lists from the store. A copy older than the operator's own
// write is read again from the API server before the sweep writes the
// absence, so the one write lands and is not refused.
func TestTheSweepReplacesACopyOlderThanItsOwnWrite(t *testing.T) {
	api, control := ownWriteUndelivered(t)

	// The analog endpoint is gone, so the sweep finds its Sink.
	if err := control.pass(context.Background(), labEndpoints()[1:], nil, labGraph(), nil); err != nil {
		t.Fatalf("the pass failed: %v", err)
	}

	if got := sent(api, "GET"); got != 1 {
		t.Errorf("the pass sent %d reads, want 1: %v", got, api.requests)
	}
	if got := sent(api, "PATCH"); got != 1 {
		t.Errorf("the pass sent %d writes, want 1: %v", got, api.requests)
	}
	if connected := conditionOf(api.sinks[testAnalogName].Status.EndpointStatus, ConnectedCondition); connected != conditionFalse {
		t.Errorf("the Sink reports Connected %q, want the absence reported", connected)
	}
}

// A Sink the API server no longer holds, whose store copy is older than
// the operator's own write, is left out of the sweep.
func TestTheSweepLeavesOutASinkTheAPIServerNoLongerHolds(t *testing.T) {
	api, control := ownWriteUndelivered(t)
	api.mutex.Lock()
	delete(api.sinks, testAnalogName)
	api.mutex.Unlock()

	if err := control.pass(context.Background(), labEndpoints()[1:], nil, labGraph(), nil); err != nil {
		t.Fatalf("the pass failed: %v", err)
	}

	if got := sent(api, "PATCH"); got != 0 {
		t.Errorf("the pass sent %d writes, want none: %v", got, api.requests)
	}
	if _, held := api.sinks[testAnalogName]; held {
		t.Error("the sweep created the Sink again")
	}
}

// conditionOf answers the status of one condition, or nothing.
func conditionOf(status EndpointStatus, kind string) string {
	for _, held := range status.Conditions {
		if held.Type == kind {
			return held.Status
		}
	}
	return ""
}

// A copy in the store that does not convert is read from the API
// server, so the sweep still reports the absence of its Sink instead of
// leaving the Sink out.
func TestTheSweepReadsACopyThatDoesNotConvertFromTheAPIServer(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	withMemos(control)
	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatal(err)
	}
	delivered(t, api, control)
	broken := asObject(t, *api.sinks[testAnalogName])
	if err := unstructured.SetNestedField(broken.Object, "loud", "spec", "volume"); err != nil {
		t.Fatal(err)
	}
	if err := control.cache.sinks.View.Store.Update(broken); err != nil {
		t.Fatal(err)
	}

	if err := control.pass(context.Background(), labEndpoints()[1:], nil, labGraph(), nil); err != nil {
		t.Fatalf("the pass failed: %v", err)
	}

	if connected := conditionOf(api.sinks[testAnalogName].Status.EndpointStatus, ConnectedCondition); connected != conditionFalse {
		t.Errorf("the Sink reports Connected %q, want the absence reported", connected)
	}
}
