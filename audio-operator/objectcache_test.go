package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// cacheOf returns an objectCache whose stores hold the fixture's
// current resources, as if the watches had received every write.
func cacheOf(t *testing.T, api *endpointAPI) objectCache {
	t.Helper()
	sinks, sources := cache.NewStore(cache.MetaNamespaceKeyFunc), cache.NewStore(cache.MetaNamespaceKeyFunc)
	api.mutex.Lock()
	defer api.mutex.Unlock()
	for _, sink := range api.sinks {
		if err := sinks.Add(asObject(t, *sink)); err != nil {
			t.Fatalf("adding the Sink to the store: %v", err)
		}
	}
	for _, source := range api.sources {
		if err := sources.Add(asObject(t, *source)); err != nil {
			t.Fatalf("adding the Source to the store: %v", err)
		}
	}
	synced := func() bool { return true }
	return objectCache{
		sinks:   informer.Held{View: informer.View{Store: sinks, Synced: synced}},
		sources: informer.Held{View: informer.View{Store: sources, Synced: synced}},
	}
}

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

// A pass that changes nothing, over the lab card's endpoints and a
// speaker, sends one GET per endpoint when it reads the API server, and
// none when it reads the stores. When the set of endpoints changes, the
// pass also sweeps. Without stores, the sweep lists the Sinks and the
// Sources from the API server. With stores that have finished their
// first list, it sends no GET. The memos of the first pass's writes
// cause no read, because the stores have those writes.
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
				t.Fatalf("running the pass: %v", err)
			}
			if c.cached {
				fillStores(t, api, control)
			}
			speakers := testSpeakers()
			if c.sweep {
				// With the speaker gone, the endpoint set differs from the
				// one the last sweep saw.
				speakers = nil
			}
			clearRequests(api)

			if err := control.pass(ctx, labEndpoints(), speakers, labGraph(), nil); err != nil {
				t.Fatalf("running the pass: %v", err)
			}
			if got := reads(api); got != c.want {
				t.Errorf("the pass sent %d reads, want %d: %v", got, c.want, api.requests)
			}
		})
	}
}

// When a store's copy is older than the API server's, the status patch
// computed from it fails with a conflict. The pass then reads the
// resource from the API server and patches again.
func TestAStatusWriteFromAnOlderCopyReadsAgainAndLands(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	ctx := context.Background()
	if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	control.cache = cacheOf(t, api)
	// Raise every resourceVersion in the fixture without updating the
	// store, so each store copy is older.
	api.mutex.Lock()
	for _, sink := range api.sinks {
		sink.Metadata.ResourceVersion = api.nextVersion()
	}
	for _, source := range api.sources {
		source.Metadata.ResourceVersion = api.nextVersion()
	}
	api.mutex.Unlock()
	control.now = func() time.Time { return factsTime.Add(time.Hour) }

	// The capture endpoint is gone, so the sweep patches its Source
	// from the older copy.
	if err := control.pass(ctx, labEndpoints()[:1], nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	for _, condition := range api.sources[testSourceName].Status.Conditions {
		if condition.Type == ConnectedCondition && condition.Status != conditionFalse {
			t.Errorf("the Source reports %s %s, want the absence reported", condition.Type, condition.Status)
		}
	}
	if !strings.Contains(strings.Join(api.requests, " "), "GET "+SourcesPath+"/"+testSourceName) {
		t.Errorf("the pass sent %v, want a GET of the Source after the conflict", api.requests)
	}
}

// withMemos gives the pass the version memos but no stores, so every
// read goes to the API server.
func withMemos(control *endpointControl) {
	control.cache = objectCache{
		sinks:   informer.Held{Versions: memo.New()},
		sources: informer.Held{Versions: memo.New()},
	}
}

// fillStores fills the stores from the fixture, as if the watches had
// received every write so far, and keeps the pass's memos.
func fillStores(t *testing.T, api *endpointAPI, control *endpointControl) {
	t.Helper()
	memos := control.cache
	control.cache = cacheOf(t, api)
	control.cache.sinks.Versions = memos.sinks.Versions
	control.cache.sources.Versions = memos.sources.Versions
}

func clearRequests(api *endpointAPI) {
	api.mutex.Lock()
	defer api.mutex.Unlock()
	api.requests = nil
}

// staleCache runs a first pass with the memos, fills the stores from
// the fixture, and then raises every resourceVersion in the fixture, as
// a write by another client would. Every store copy is then older than
// the API server's, and the memos do not have the new versions.
func staleCache(t *testing.T, api *endpointAPI, control *endpointControl) {
	t.Helper()
	withMemos(control)
	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	fillStores(t, api, control)
	api.mutex.Lock()
	defer api.mutex.Unlock()
	for _, sink := range api.sinks {
		sink.Metadata.ResourceVersion = api.nextVersion()
	}
	for _, source := range api.sources {
		source.Metadata.ResourceVersion = api.nextVersion()
	}
}

// A list before the Sink watch has finished its first list comes from
// the API server. The store can then become ready with an older copy,
// because the watch's first list can come from the API server's watch
// cache, which can lag behind etcd. The next list must not return that
// older copy. If it did, a pass that saw the claim in the first list
// would then see the Sink without the claim and treat it as free.
func TestAListBeforeTheStoreIsReadyIsNeverReadOlder(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	api.sinks[testSinkName] = &Sink{Metadata: EndpointMeta{Name: testSinkName, ResourceVersion: api.nextVersion()},
		Status: sinkStatus(EndpointStatus{Node: "liken-1"})}
	older := cacheOf(t, api).sinks.View
	api.sinks[testSinkName].Status.Claims = []EndpointClaim{{Namespace: "media", Name: "den"}}
	api.sinks[testSinkName].Metadata.ResourceVersion = api.nextVersion()
	ready := false
	control.cache.sinks = informer.Held{View: informer.View{Store: older.Store, Synced: func() bool { return ready }}, Versions: memo.New()}

	listed, err := control.readSinks()
	if err != nil || len(listed) != 1 || len(listed[0].Status.Claims) == 0 {
		t.Fatalf("the list before the store is ready = %+v, %v; want the claim", listed, err)
	}
	ready = true
	read, err := control.readSinks()

	if err != nil || len(read) != 1 || len(read[0].Status.Claims) == 0 {
		t.Errorf("the list from the ready store = %+v, %v; want the claim, not the store's older copy", read, err)
	}
}

// A claim on the analog sink changes its status. The patch computed
// from the store's older copy conflicts, and the pass reads the Sink
// from the API server and writes the claim.
func TestAReconcileFromAnOlderCopyReadsAgainAndLands(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	staleCache(t, api, control)
	control.claims.record("claim-1", EndpointClaim{Namespace: "media", Name: "den"}, []string{testAnalogName})

	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	if claims := api.sinks[testAnalogName].Status.Claims; len(claims) != 1 || claims[0].Name != "den" {
		t.Errorf("the analog sink's claims are %+v, want den", claims)
	}
}

// When another machine has taken a Sink since the store's copy (a
// Bluetooth speaker that moved, for example), this machine's sweep
// leaves it alone, even after a conflict makes the sweep read it from
// the API server.
func TestTheSweepLeavesASinkAnotherMachineTook(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	staleCache(t, api, control)
	api.mutex.Lock()
	api.sinks[testAnalogName].Status.Node = "node-2"
	taken := api.sinks[testAnalogName].Status
	api.mutex.Unlock()

	// The analog endpoint is gone from this machine, and the store's
	// copy of its Sink still has this machine's name, so the sweep
	// considers it.
	if err := control.pass(context.Background(), labEndpoints()[1:], nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	if got := api.sinks[testAnalogName].Status; got.NodeName != taken.NodeName || got.Node != "node-2" {
		t.Errorf("the other machine's Sink is %+v after the sweep, want it unchanged on node-2", got)
	}
}

// A Sink deleted after the store received its copy is created again
// when the pass has a status to write. A pass with nothing to write
// sends nothing, and the watch's delete event removes the copy from the
// store and starts a pass that creates the Sink.
func TestASinkDeletedSinceTheStoresCopyIsCreatedAgain(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	staleCache(t, api, control)
	api.mutex.Lock()
	delete(api.sinks, testAnalogName)
	api.mutex.Unlock()
	control.claims.record("claim-1", EndpointClaim{Namespace: "media", Name: "den"}, []string{testAnalogName})

	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	if sink, found := api.sinks[testAnalogName]; !found || sink.Status.Node != "liken-1" {
		t.Errorf("the analog sink is %+v, %v; want it created again with its status", sink, found)
	}
}

// The store still has the Sink's copy from before the operator wrote a
// claim into its status. When the claim is released, the status the
// pass composes equals that copy, so without the memo the pass would
// skip the write and leave the released claim in the status. The memo
// has the version of the operator's own write, so the pass reads the
// Sink from the API server and writes the release.
func TestAPassDoesNotActOnACopyOlderThanItsOwnWrite(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	withMemos(control)
	ctx := context.Background()
	if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	fillStores(t, api, control)
	control.claims.record("claim-1", EndpointClaim{Namespace: "media", Name: "den"}, []string{testAnalogName})
	if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	if claims := api.sinks[testAnalogName].Status.Claims; len(claims) == 0 {
		t.Fatal("the analog Sink reports no claim after the pass, want the claim")
	}

	control.claims.release("claim-1")
	clearRequests(api)
	if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}

	if got := reads(api); got != 1 {
		t.Errorf("the pass sent %d reads, want 1, of the Sink it wrote: %v", got, api.requests)
	}
	if claims := api.sinks[testAnalogName].Status.Claims; len(claims) != 0 {
		t.Errorf("the Sink reports the released claims %+v, want none", claims)
	}
}

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

// claimWriteNotInStore runs a pass that creates the lab card's
// resources, fills the stores, and runs a second pass that writes a
// claim onto the analog Sink. The Sink store's copy is then older than
// the operator's own write.
func claimWriteNotInStore(t *testing.T) (*endpointAPI, *endpointControl) {
	t.Helper()
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	withMemos(control)
	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	fillStores(t, api, control)
	control.claims.record("claim-1", EndpointClaim{Namespace: "media", Name: "den"}, []string{testAnalogName})
	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	clearRequests(api)
	return api, control
}

// The sweep lists from the store, and reads a copy older than the
// operator's own write from the API server before it writes the
// absence, so its patch succeeds with no conflict.
func TestTheSweepReplacesACopyOlderThanItsOwnWrite(t *testing.T) {
	api, control := claimWriteNotInStore(t)

	// The analog endpoint is gone, so the sweep finds its Sink.
	if err := control.pass(context.Background(), labEndpoints()[1:], nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
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

// The Sink was deleted after the operator's own write, and the store
// still has an older copy. The sweep sends no patch for it and does not
// create it again.
func TestTheSweepLeavesOutASinkTheAPIServerNoLongerHolds(t *testing.T) {
	api, control := claimWriteNotInStore(t)
	api.mutex.Lock()
	delete(api.sinks, testAnalogName)
	api.mutex.Unlock()

	if err := control.pass(context.Background(), labEndpoints()[1:], nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}

	if got := sent(api, "PATCH"); got != 0 {
		t.Errorf("the pass sent %d writes, want none: %v", got, api.requests)
	}
	if _, found := api.sinks[testAnalogName]; found {
		t.Error("the sweep created the Sink again, want it left out")
	}
}

func conditionOf(status EndpointStatus, kind string) conditions.Status {
	for _, condition := range status.Conditions {
		if condition.Type == kind {
			return condition.Status
		}
	}
	return ""
}

// A store copy that fails to convert to a Sink is read from the API
// server instead, so the sweep still reports the Sink absent and does
// not skip it.
func TestTheSweepReadsACopyThatDoesNotConvertFromTheAPIServer(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	withMemos(control)
	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	fillStores(t, api, control)
	broken := asObject(t, *api.sinks[testAnalogName])
	if err := unstructured.SetNestedField(broken.Object, "loud", "spec", "volume"); err != nil {
		t.Fatalf("setting spec.volume on the store's copy: %v", err)
	}
	if err := control.cache.sinks.View.Store.Update(broken); err != nil {
		t.Fatalf("replacing the Sink in the store: %v", err)
	}

	if err := control.pass(context.Background(), labEndpoints()[1:], nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}

	if connected := conditionOf(api.sinks[testAnalogName].Status.EndpointStatus, ConnectedCondition); connected != conditionFalse {
		t.Errorf("the Sink reports Connected %q, want the absence reported", connected)
	}
}
