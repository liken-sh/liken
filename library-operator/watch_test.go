//go:build !pod

package main

// These tests run the eight watches through client-go's real reflector,
// against the fake cluster's watch streams. The reflector's own loop is
// upstream's to test; what these tests prove is that each collection
// reaches the pass from the right path, that a change wakes the pass
// only when the pass acts on it, and that an object that does not
// convert is reported.
//
// Each test runs in a synctest bubble, so a change has reached the pass
// when every goroutine in the bubble is blocked, and the test reads the
// wake at that moment instead of waiting for one that may not come.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

// The bound on every wait in this file. The tests run on the fake clock,
// so the bound costs no real time. It exceeds the backoff client-go's
// reflector waits before it opens a watch again.
const watchTimeout = 5 * time.Second

// watchedCluster starts the eight watches on a fake cluster, and
// answers them once every collection has been read. The wakes of the
// first read are drained, so a test reads only the wakes its own
// changes cause. The caller runs in a synctest bubble.
func watchedCluster(t *testing.T, cluster *fakeCluster, m *metrics) (*watches, chan struct{}) {
	t.Helper()
	return watchedServer(t, cluster.start(t), m)
}

// watchedServer starts the eight watches on one server.
func watchedServer(t *testing.T, server *apiservertest.Server, m *metrics) (*watches, chan struct{}) {
	t.Helper()
	client, err := dynamic.NewForConfig(server.Config())
	if err != nil {
		t.Fatal(err)
	}
	wake := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	watched := startWatches(ctx, client, wake, m,
		apiclient.New(apiservertest.Host, server.Client(), ""), newObjectVersions())
	t.Cleanup(func() {
		cancel()
		watched.wait()
	})
	settling, settled := context.WithTimeout(context.Background(), watchTimeout)
	defer settled()
	if err := watched.settle(settling); err != nil {
		t.Fatal(err)
	}
	wokeOnceIdle(wake)
	return watched, wake
}

// wokeOnceIdle answers whether the pass was woken, once every goroutine in the
// bubble has finished the work the last change started. It takes the
// wake, so the next call reads only what follows.
func wokeOnceIdle(wake <-chan struct{}) bool {
	synctest.Wait()
	select {
	case <-wake:
		return true
	default:
		return false
	}
}

// Each watch reads its own collection, and the pass reads each object
// as the operator's struct. The pod watch reads only the pods that hold
// a catalog agent.
func TestEachWatchAnswersThePassWithItsCollection(t *testing.T) {
	member := readyCatalogPod("house", "house")
	cases := []struct {
		name string
		read func(ctx context.Context, watched *watches) (string, error)
		want string
	}{
		{name: "libraries", want: "movies /movies", read: func(ctx context.Context, watched *watches) (string, error) {
			list, err := watched.readLibraries(ctx)
			return joined(list.Items, func(l Library) string { return l.Metadata.Name + " " + l.Spec.Storage.Root }), err
		}},
		{name: "catalogs", want: "house", read: func(ctx context.Context, watched *watches) (string, error) {
			list, err := watched.readCatalogs(ctx)
			return joined(list.Items, func(c NamespaceCatalog) string { return c.Metadata.Name }), err
		}},
		{name: "member pods", want: member.Metadata.Name + " 10.42.0.9", read: func(_ context.Context, watched *watches) (string, error) {
			list, err := watched.readMemberPods()
			return joined(list.Items, func(p Pod) string { return p.Metadata.Name + " " + p.Status.PodIP }), err
		}},
		{name: "players", want: "den true", read: func(_ context.Context, watched *watches) (string, error) {
			list, err := watched.readPlayers()
			return joined(list.Items, func(p Player) string {
				if p.delegated() {
					return p.Metadata.Name + " true"
				}
				return p.Metadata.Name
			}), err
		}},
		{name: "media preferences", want: "Europe/Lisbon", read: func(_ context.Context, watched *watches) (string, error) {
			list, err := watched.readMediaPreferences()
			return householdZone(list), err
		}},
		{name: "metadata providers", want: "house/tmdb", read: func(ctx context.Context, watched *watches) (string, error) {
			list, err := watched.readMetadataProviders(ctx)
			return joined(list.Items, func(p MetadataProvider) string { return p.Metadata.Namespace + "/" + p.Metadata.Name }), err
		}},
		{name: "plays", want: "den-b2k9x", read: func(_ context.Context, watched *watches) (string, error) {
			list, err := watched.readPlays()
			return joined(list.Items, func(p Play) string { return p.Metadata.Name }), err
		}},
		{name: "people", want: "Person A", read: func(_ context.Context, watched *watches) (string, error) {
			list, err := watched.readPeople()
			return joined(list.Items, func(p Person) string { return p.Spec.DisplayName }), err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := newFakeCluster()
				boundHouse(cluster)
				cluster.pods[member.Metadata.Name] = member
				cluster.pods[testOperatorPod] = operatorPod(testScannerImage)
				seedPlayer(cluster, "den", "house", screenController)
				cluster.preferences = &MediaPreferences{Metadata: ObjectMeta{Name: "default"},
					Spec: MediaPreferencesSpec{TimeZone: "Europe/Lisbon"}}
				cluster.providers["tmdb"] = &MetadataProvider{Metadata: ObjectMeta{Name: "tmdb", Namespace: "house"}}
				cluster.plays = []Play{{Metadata: ObjectMeta{Name: "den-b2k9x", Namespace: "house"}}}
				cluster.people["person-a"] = &Person{Metadata: ObjectMeta{Name: "person-a"}, Spec: PersonSpec{DisplayName: "Person A"}}
				watched, _ := watchedCluster(t, cluster, nil)

				got, err := c.read(t.Context(), watched)

				if err != nil || got != c.want {
					t.Errorf("read = %q, %v; want %q", got, err, c.want)
				}
			})
		})
	}
}

func joined[T any](items []T, describe func(T) string) string {
	described := make([]string, 0, len(items))
	for _, item := range items {
		described = append(described, describe(item))
	}
	return strings.Join(described, ", ")
}

// A change wakes the pass only when the pass acts on it. An edit to a
// Library, a Catalog, or a MetadataProvider is a change to its spec or
// its deletion mark; this operator's own status writes and finalizer
// patches wake nothing. A change to an object another writer owns wakes
// the pass when a field the operator reads changed, and a write that
// changed only the resourceVersion wakes nothing.
func TestAWatchWakesThePassOnlyForAChangeItActsOn(t *testing.T) {
	cases := []struct {
		name   string
		change func(cluster *fakeCluster)
		wakes  bool
	}{
		{name: "a new Library", wakes: true, change: func(cluster *fakeCluster) {
			boundStudio(cluster)
		}},
		{name: "an edit to a Library's spec", wakes: true, change: func(cluster *fakeCluster) {
			cluster.libraries["movies"].Metadata.Generation++
			cluster.libraries["movies"].Spec.Storage.Root = "/films"
		}},
		{name: "a Library marked for deletion", wakes: true, change: func(cluster *fakeCluster) {
			cluster.libraries["movies"].Metadata.DeletionTimestamp = "2026-09-27T12:00:00Z"
		}},
		{name: "a Library deleted and created again", wakes: true, change: func(cluster *fakeCluster) {
			cluster.libraries["movies"].Metadata.UID = "another-uid"
		}},
		{name: "a removed Library", wakes: true, change: func(cluster *fakeCluster) {
			delete(cluster.libraries, "movies")
		}},
		{name: "the operator's status write on a Library", wakes: false, change: func(cluster *fakeCluster) {
			cluster.libraries["movies"].Status.Conditions = []Condition{{Type: conditionReady, Status: "True"}}
		}},
		{name: "the operator's finalizer on a Library", wakes: false, change: func(cluster *fakeCluster) {
			cluster.libraries["movies"].Metadata.Finalizers = []string{libraryFinalizer}
		}},
		{name: "the operator's status write on a Catalog", wakes: false, change: func(cluster *fakeCluster) {
			cluster.catalogs["house"].Status.Conditions = []Condition{{Type: conditionReady, Status: "True"}}
		}},
		{name: "an edit to a Catalog's spec", wakes: true, change: func(cluster *fakeCluster) {
			cluster.catalogs["house"].Metadata.Generation++
		}},
		{name: "a Player whose idle controller changed", wakes: true, change: func(cluster *fakeCluster) {
			cluster.players["den"].Status.Idle.Controller = "media.liken.sh/idle-screen"
		}},
		{name: "a Player written with nothing the operator reads", wakes: false, change: func(cluster *fakeCluster) {
			cluster.players["den"].Metadata.ResourceVersion = "31"
		}},
		{name: "a member pod whose reporter is no longer ready", wakes: true, change: func(cluster *fakeCluster) {
			cluster.pods["house-catalog-0"].Status.ContainerStatuses[0].Ready = false
		}},
		{name: "a person's finalizer", wakes: true, change: func(cluster *fakeCluster) {
			cluster.people["person-a"].Metadata.Finalizers = []string{progressFinalizer}
		}},
		{name: "a claim whose phase changes", wakes: true, change: func(cluster *fakeCluster) {
			cluster.claims["movies"].Status.Phase = "Lost"
		}},
		{name: "a worker Job that finishes", wakes: true, change: func(cluster *fakeCluster) {
			cluster.jobs["house/movies-walk-a"].Status.Succeeded = 1
		}},
		{name: "the operator's own write of its Service", wakes: false, change: func(cluster *fakeCluster) {
			cluster.services["house/catalog"].Spec.PublishNotReadyAddresses = false
		}},
		{name: "a Service somebody deleted", wakes: true, change: func(cluster *fakeCluster) {
			delete(cluster.services, "house/catalog")
		}},
		{name: "a ResourceClaimTemplate the cluster owner writes", wakes: true, change: func(cluster *fakeCluster) {
			cluster.claimTemplates["house/appearances-gpu"] = ownersTemplate("appearances-gpu")
		}},
		{name: "a ResourceClaimTemplate the cluster owner deletes", wakes: true, change: func(cluster *fakeCluster) {
			delete(cluster.claimTemplates, "house/trickplay-gpu")
		}},
		{name: "a new label on a ResourceClaimTemplate", wakes: false, change: func(cluster *fakeCluster) {
			cluster.claimTemplates["house/trickplay-gpu"].Metadata.Labels = map[string]string{"team": "media"}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := newFakeCluster()
				boundHouse(cluster)
				member := readyCatalogPod("house", "house")
				cluster.pods[member.Metadata.Name] = member
				seedPlayer(cluster, "den", "house", screenController)
				cluster.people["person-a"] = &Person{Metadata: ObjectMeta{Name: "person-a"}}
				cluster.jobs["house/movies-walk-a"] = &Job{Metadata: ObjectMeta{Name: "movies-walk-a", Namespace: "house",
					Labels: map[string]string{scannerLabelKey: workerLabelValue}}}
				cluster.services["house/catalog"] = buildCatalogService("house", nil)
				cluster.claimTemplates["house/trickplay-gpu"] = ownersTemplate("trickplay-gpu")
				_, wake := watchedCluster(t, cluster, nil)

				cluster.mutex.Lock()
				c.change(cluster)
				cluster.mutex.Unlock()
				cluster.announce()

				if got := wokeOnceIdle(wake); got != c.wakes {
					t.Errorf("woke = %v, want %v", got, c.wakes)
				}
			})
		})
	}
}

// A cluster that runs no media-operator serves no Players. The watch
// settles all the same, and the pass reads the failure, which it takes
// as no Players.
func TestAWatchOnACollectionNobodyServesSettlesAndAnswersItsFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		boundHouse(cluster)
		cluster.broken[playersPath] = http.StatusNotFound
		watched, _ := watchedCluster(t, cluster, nil)

		players, err := watched.readPlayers()

		if err == nil || len(players.Items) != 0 {
			t.Errorf("players = %+v, %v; want none and the failure", players.Items, err)
		}
		if _, err := watched.readLibraries(t.Context()); err != nil {
			t.Errorf("the libraries = %v, want them read", err)
		}
	})
}

// A watch the API server ends is opened again, and each reopen counts
// one watch_restarts_total under the kind the watch serves. The first
// watch counts none, because it opened nothing again.
func TestAReopenedWatchCountsOneRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		boundHouse(cluster)
		m := newMetrics("test")
		watchedCluster(t, cluster, m)
		if got := testutil.ToFloat64(m.watchRestarts.WithLabelValues(kindLibrary)); got != 0 {
			t.Fatalf("watch_restarts_total = %v before any reopen, want 0", got)
		}

		cluster.cutStreams()
		time.Sleep(watchTimeout)
		synctest.Wait()

		if got := testutil.ToFloat64(m.watchRestarts.WithLabelValues(kindLibrary)); got != 1 {
			t.Errorf("watch_restarts_total = %v after one reopen, want 1", got)
		}
	})
}

// An object that does not convert to the operator's struct is an
// error that names the object, and the read of its collection fails
// with it, the way one object that does not decode fails a list. A
// tombstone, which the informer hands a handler for an object deleted
// while the watch was down, converts as the object it holds.
func TestAnObjectThatDoesNotConvertIsAnErrorThatNamesIt(t *testing.T) {
	good := Library{APIVersion: libraryAPIVersion, Kind: "Library",
		Metadata: ObjectMeta{Name: "movies", Namespace: "house", Generation: 2}}
	mistyped := asObject(t, good)
	if err := unstructured.SetNestedField(mistyped.Object, "two", "metadata", "generation"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		object  any
		wantErr string
	}{
		{name: "an object", object: asObject(t, good)},
		{name: "a tombstone", object: cache.DeletedFinalStateUnknown{Key: "house/movies", Obj: asObject(t, good)}},
		{name: "a field of the wrong type", object: mistyped, wantErr: "Library house/movies does not convert"},
		{name: "a tombstone with no copy", object: cache.DeletedFinalStateUnknown{Key: "house/movies"}, wantErr: "the tombstone for house/movies holds no copy"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := informer.Convert[Library](c.object)
			if c.wantErr == "" && (err != nil || got.Metadata.Generation != 2) {
				t.Fatalf("convert = %+v, %v; want generation 2 and no error", got.Metadata, err)
			}
			if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
				t.Fatalf("convert error = %v, want one that says %q", err, c.wantErr)
			}
		})
	}
}

// A change whose object does not convert still wakes the pass, and the
// handler reports it, because nothing says what the change was. A
// tombstone that holds no copy of the object wakes the pass the same way.
func TestAChangeThatDoesNotConvertWakesThePass(t *testing.T) {
	good := Library{Metadata: ObjectMeta{Name: "movies", Namespace: "house", Generation: 2}}
	mistyped := asObject(t, good)
	if err := unstructured.SetNestedField(mistyped.Object, "two", "metadata", "generation"); err != nil {
		t.Fatal(err)
	}
	client, err := dynamic.NewForConfig(newFakeCluster().start(t).Config())
	if err != nil {
		t.Fatal(err)
	}
	libraries := newCollection(t.Context(), client, make(chan struct{}, 1), nil, "libraries", kindLibrary, libraryResource, "",
		edited(func(library *Library) *ObjectMeta { return &library.Metadata }))
	cases := []struct {
		name   string
		change func(wake func())
	}{
		{name: "an added object", change: func(wake func()) { libraries.added(mistyped, wake) }},
		{name: "an update to an object", change: func(wake func()) { libraries.updated(asObject(t, good), mistyped, wake) }},
		{name: "an update from a held copy", change: func(wake func()) { libraries.updated(mistyped, asObject(t, good), wake) }},
		{name: "an empty tombstone", change: func(wake func()) {
			libraries.removed(cache.DeletedFinalStateUnknown{Key: "house/movies"}, wake)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			woke := false

			c.change(func() { woke = true })

			if !woke {
				t.Error("the change woke no pass")
			}
		})
	}
}

// asObject is an object the way the informer hands it to a handler.
func asObject[T any](t *testing.T, item T) *unstructured.Unstructured {
	t.Helper()
	fields, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&item)
	if err != nil {
		t.Fatal(err)
	}
	return &unstructured.Unstructured{Object: fields}
}

// The informer stores each Node without status.images, which the
// operator never reads, and leaves every other kind as it came.
func TestTheWatchesDropANodesImages(t *testing.T) {
	cases := []struct {
		kind       string
		wantImages bool
	}{
		{"Node", false},
		{"Pod", true},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			object := &unstructured.Unstructured{Object: map[string]any{
				"kind":     c.kind,
				"metadata": map[string]any{"name": "node-1"},
				"status":   map[string]any{"images": []any{map[string]any{"sizeBytes": int64(1)}}},
			}}

			dropNodeImages(object)

			_, held, _ := unstructured.NestedSlice(object.Object, "status", "images")
			if held != c.wantImages {
				t.Errorf("status.images held = %v, want %v", held, c.wantImages)
			}
		})
	}
}

// A pass reads every watched collection from the informers and sends no
// list for any of them. A pass that finds nothing changed writes nothing,
// so its own writes wake no pass after it. The two Catalogs name no
// Jellyfin server, so the pass sends no read or delete for the backfill
// Job or the Jellyfin Service. It reads the template the Library's
// trickplay worker names, and the names of the templates an earlier
// release created, from the watch: a settled pass sends the API server
// nothing. The same pass sent 20 requests when it listed the claims, the
// volumes, the pods, the Jobs, and the nodes, and read the Services and
// the slices by name.
func TestASteadyPassListsNoWatchedCollectionAndWakesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		boundHouse(cluster).Spec.Trickplay = LibraryTrickplay{Enabled: true, GPUResourceClaimTemplate: "trickplay-gpu"}
		cluster.claimTemplates["house/trickplay-gpu"] = ownersTemplate("trickplay-gpu")
		boundStudio(cluster)
		for _, namespace := range []string{"house", "studio"} {
			pod := readyCatalogPod(namespace, namespace)
			cluster.pods[pod.Metadata.Name] = pod
		}
		seedPlayer(cluster, "den", "house", screenController)
		cluster.people["person-a"] = &Person{Metadata: ObjectMeta{Name: "person-a", UID: "person-a-uid"}}
		operator := testOperator(t, cluster)
		watched, wake := watchedCluster(t, cluster, nil)
		operator.watched = watched
		// The first passes stand what the cluster lacks. Each write reaches
		// the informers before the next pass reads them.
		for range 3 {
			operator.pass()
			wokeOnceIdle(wake)
		}
		cluster.mutex.Lock()
		cluster.requests = nil
		cluster.mutex.Unlock()

		operator.pass()

		sent := cluster.requestLines()
		for _, line := range sent {
			if _, listed := watchedKinds[strings.TrimPrefix(line, http.MethodGet+" ")]; listed {
				t.Errorf("the pass sent %s, want every watched collection read from its store", line)
			}
		}
		if len(sent) != 0 {
			t.Errorf("a steady pass sent %d requests, want none: %v", len(sent), sent)
		}
		if wokeOnceIdle(wake) {
			t.Error("a steady pass woke the next one")
		}
	})
}

// A collection of another operator that does not answer within the
// bound reads as not read yet, and the operator starts on the others.
// A collection the operator needs that does not answer ends the start.
func TestSettleWaitsOnlyForTheCollectionsTheOperatorNeeds(t *testing.T) {
	cases := []struct {
		name    string
		silent  string
		settles bool
	}{
		{name: "the players", silent: playersPath, settles: true},
		{name: "the libraries", silent: librariesPath, settles: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := newFakeCluster()
				boundHouse(cluster)
				answering := cluster.handler()
				server := apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == c.silent {
						<-r.Context().Done()
						return
					}
					answering.ServeHTTP(w, r)
				}))
				client, err := dynamic.NewForConfig(server.Config())
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				watched := startWatches(ctx, client, make(chan struct{}, 1), nil,
					apiclient.New(apiservertest.Host, server.Client(), ""), newObjectVersions())
				t.Cleanup(func() {
					cancel()
					watched.wait()
				})
				bound, done := context.WithTimeout(context.Background(), watchTimeout)
				defer done()

				err = watched.settle(bound)

				if settled := err == nil; settled != c.settles {
					t.Errorf("settle = %v, want settled %v", err, c.settles)
				}
			})
		})
	}
}
