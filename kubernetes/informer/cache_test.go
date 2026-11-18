package informer

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// fakeAPI serves reads and status writes of things like the API server:
// a status write with an old resourceVersion gets 409, and each
// successful write sets a new version. While failing is true, every
// request gets 500.
type fakeAPI struct {
	mu       sync.Mutex
	objects  map[string]thing
	version  int
	failing  bool
	requests []string
}

func newFakeAPI(things ...thing) *fakeAPI {
	api := &fakeAPI{objects: map[string]thing{}, version: 100}
	for _, item := range things {
		api.objects[Key(&item.Metadata)] = item
	}
	return api
}

const fakeThingsPath = "/things"

func thingPath(key string) string { return fakeThingsPath + "/" + key }

func (api *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.requests = append(api.requests, r.Method+" "+r.URL.Path)
	if api.failing {
		http.Error(w, "etcd is gone", http.StatusInternalServerError)
		return
	}
	if r.URL.Path == fakeThingsPath {
		keys := slices.Sorted(maps.Keys(api.objects))
		items := make([]thing, 0, len(keys))
		for _, key := range keys {
			items = append(items, api.objects[key])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		return
	}
	key, status := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/things/"), "/status")
	stored, found := api.objects[key]
	if !found {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPut && status {
		var written thing
		_ = json.NewDecoder(r.Body).Decode(&written)
		if written.Metadata.ResourceVersion != stored.Metadata.ResourceVersion {
			w.WriteHeader(http.StatusConflict)
			return
		}
		api.version++
		stored.Status = written.Status
		stored.Metadata.ResourceVersion = fmt.Sprint(api.version)
		api.objects[key] = stored
	}
	_ = json.NewEncoder(w).Encode(stored)
}

// put writes a thing as another writer would, and returns it with its
// new version.
func (api *fakeAPI) put(item thing) thing {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.version++
	item.Metadata.ResourceVersion = fmt.Sprint(api.version)
	api.objects[Key(&item.Metadata)] = item
	return item
}

// sent returns the requests received since the last call.
func (api *fakeAPI) sent() []string {
	api.mu.Lock()
	defer api.mu.Unlock()
	requests := api.requests
	api.requests = nil
	return requests
}

func testClient(t *testing.T, api *fakeAPI) *apiclient.Client {
	t.Helper()
	server := apiservertest.Start(t, api)
	return apiclient.New(apiservertest.Host, server.Client(), "")
}

// storeOf returns a ready View over a store that holds the objects.
func storeOf(t *testing.T, objects ...any) View {
	t.Helper()
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	for _, object := range objects {
		if item, ok := object.(thing); ok {
			object = asObject(t, item)
		}
		if err := store.Add(object); err != nil {
			t.Fatalf("adding the object to the store: %v", err)
		}
	}
	return View{Store: store, Synced: func() bool { return true }}
}

// mistyped is an object whose spec does not convert to a thing.
func mistyped(t *testing.T, name string) *unstructured.Unstructured {
	t.Helper()
	object := asObject(t, newThing(name, "5", 1))
	if err := unstructured.SetNestedField(object.Object, "large", "spec", "size"); err != nil {
		t.Fatalf("setting spec.size to a string: %v", err)
	}
	return object
}

func TestCachedReturnsOnlyAConvertibleCopyOfAReadyStore(t *testing.T) {
	view := storeOf(t, newThing("a", "5", 1), mistyped(t, "b"))
	notReady := view
	notReady.Synced = func() bool { return false }
	cases := []struct {
		name   string
		view   View
		key    string
		wantOK bool
	}{
		{"no store", View{}, "a", false},
		{"a store that is not ready", notReady, "a", false},
		{"a copy", view, "a", true},
		{"a copy that does not convert", view, "b", false},
		{"no copy", view, "c", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Cached[thing](c.view, c.key)
			if ok != c.wantOK || (ok && got.Metadata.Name != c.key) {
				t.Errorf("Cached(%s) = %+v, %v; want ok = %v", c.key, got, ok, c.wantOK)
			}
		})
	}
}

func TestCachedListSortsByKeyAndSkipsUnconvertibleCopies(t *testing.T) {
	view := storeOf(t, newThing("c", "5", 1), mistyped(t, "b"), newThing("a", "6", 1))

	var names []string
	for _, item := range CachedList[thing](view) {
		names = append(names, item.Metadata.Name)
	}

	if !slices.Equal(names, []string{"a", "c"}) {
		t.Errorf("CachedList = %v, want [a c]", names)
	}
}

func TestReadOneReadsTheAPIServerOnlyWhenTheStoreCopyIsNotUsable(t *testing.T) {
	cases := []struct {
		name        string
		stored      []any
		watching    bool
		noted       string
		wantReads   int
		wantVersion string
	}{
		{"a current copy", []any{newThing("a", "5", 1)}, true, "5", 0, "5"},
		{"a copy older than a write", []any{newThing("a", "5", 1)}, true, "", 1, "101"},
		{"no copy", nil, true, "", 1, "101"},
		{"a copy whose watch the API server refuses", []any{newThing("a", "5", 1)}, false, "5", 1, "101"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := newFakeAPI()
			api.put(newThing("a", "", 1))
			versions := memo.New()
			versions.Note("a", c.noted)

			view := storeOf(t, c.stored...)
			view.Synced = func() bool { return c.watching }

			got, err := ReadOne[thing](testClient(t, api), Held{View: view, Versions: versions}, "a", thingPath("a"))

			if err != nil || got.Metadata.ResourceVersion != c.wantVersion {
				t.Fatalf("ReadOne = %+v, %v; want version %s", got, err, c.wantVersion)
			}
			if reads := api.sent(); len(reads) != c.wantReads {
				t.Errorf("the read sent %v, want %d reads", reads, c.wantReads)
			}
			if !versions.Current("a", c.wantVersion) {
				t.Errorf("the memo has no record of version %s", c.wantVersion)
			}
		})
	}
}

// c is in the store but not on the API server, so the 404 leaves it out
// of the list.
func TestCurrentListReplacesOlderCopiesWithReadsFromTheAPIServer(t *testing.T) {
	api := newFakeAPI()
	api.put(newThing("a", "", 1))
	newer := api.put(newThing("b", "", 2))
	view := storeOf(t, newThing("a", "101", 1), newThing("b", "90", 1), newThing("c", "91", 1))
	versions := memo.New()
	versions.Note("b", "")
	versions.Note("c", "")

	list, err := CurrentList[thing](testClient(t, api), Held{View: view, Versions: versions}, thingPath)

	if err != nil {
		t.Fatalf("CurrentList: %v", err)
	}
	if len(list) != 2 || list[0].Metadata.ResourceVersion != "101" || list[1].Metadata.ResourceVersion != newer.Metadata.ResourceVersion {
		t.Errorf("the list = %+v, want a at 101 and b at %s", list, newer.Metadata.ResourceVersion)
	}
	if reads := api.sent(); !slices.Equal(reads, []string{"GET /things/b", "GET /things/c"}) {
		t.Errorf("the list sent %v, want reads of b and c", reads)
	}
}

// The store becomes ready at an older version than the list that came
// from the API server before it was ready, as it can when the
// reflector's first list comes from the watch cache.
func TestListNeverReturnsAnOlderCopyThanAnEarlierList(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := newFakeAPI()
		older := api.put(newThing("a", "", 1))
		newer := api.put(newThing("a", "", 2))
		view := storeOf(t, older)
		ready := false
		view.Synced = func() bool { return ready }
		watched := Held{View: view, Versions: memo.New()}
		client := testClient(t, api)

		first, err := List[thing](client, watched, fakeThingsPath, thingPath)
		if err != nil || len(first) != 1 || first[0].Metadata.ResourceVersion != newer.Metadata.ResourceVersion {
			t.Fatalf("the list before the store is ready = %+v, %v; want a at %s", first, err, newer.Metadata.ResourceVersion)
		}
		ready = true
		second, err := List[thing](client, watched, fakeThingsPath, thingPath)

		if err != nil || len(second) != 1 || second[0].Metadata.ResourceVersion != newer.Metadata.ResourceVersion {
			t.Errorf("the list from the ready store = %+v, %v; want a at %s, not the store's %s",
				second, err, newer.Metadata.ResourceVersion, older.Metadata.ResourceVersion)
		}
		if reads := api.sent(); !slices.Equal(reads, []string{"GET /things", "GET /things/a"}) {
			t.Errorf("the lists sent %v, want the list and one read of a", reads)
		}
	})
}

// The list fails instead of leaving the object out, so a pass does not
// act on a list with an object missing.
func TestCurrentListFailsWhenItCannotReadAReplacementCopy(t *testing.T) {
	api := newFakeAPI()
	api.failing = true
	versions := memo.New()
	versions.Note("a", "")

	_, err := CurrentList[thing](testClient(t, api), Held{View: storeOf(t, newThing("a", "5", 1)), Versions: versions}, thingPath)

	if err == nil || !strings.Contains(err.Error(), "etcd is gone") {
		t.Errorf("err = %v, want the API server's failure", err)
	}
}

// A list that left out an object the operator created would make the
// pass create it again.
func TestCurrentListRetriesAnUnheldObjectAfterAFailedRead(t *testing.T) {
	api := newFakeAPI()
	created := api.put(newThing("a", "", 1))
	versions := memo.New()
	versions.Note("a", created.Metadata.ResourceVersion)
	view := storeOf(t)
	view.Whole = true
	watched := Held{View: view, Versions: versions}
	client := testClient(t, api)
	api.failing = true
	_, failed := CurrentList[thing](client, watched, thingPath)
	api.failing = false

	list, err := CurrentList[thing](client, watched, thingPath)

	if failed == nil {
		t.Error("the list succeeded although the API server refused the read of a")
	}
	if err != nil || len(list) != 1 || list[0].Metadata.ResourceVersion != created.Metadata.ResourceVersion {
		t.Errorf("the list after the failure = %+v, %v; want a at %s", list, err, created.Metadata.ResourceVersion)
	}
}

// arrivingStore adds one object to the store right after the first call
// of ListKeys, as the watch event of a create does while a pass lists.
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

func TestCurrentListIncludesACreateThatArrivesDuringTheList(t *testing.T) {
	api := newFakeAPI()
	store := &arrivingStore{Store: cache.NewStore(cache.MetaNamespaceKeyFunc), arriving: asObject(t, newThing("a", "5", 1))}
	versions := memo.New()
	versions.Note("a", "5")
	view := View{Store: store, Synced: func() bool { return true }, Whole: true}

	list, err := CurrentList[thing](testClient(t, api), Held{View: view, Versions: versions}, thingPath)

	if err != nil || len(list) != 1 {
		t.Errorf("the list = %+v, %v; want a", list, err)
	}
	if reads := api.sent(); len(reads) != 0 {
		t.Errorf("the list sent %v, want no read", reads)
	}
}

// ready is an apply function for SettleStatus that sets the Ready
// phase.
func ready(item *thing) bool {
	if item.Status.Phase == "Ready" {
		return false
	}
	item.Status.Phase = "Ready"
	return true
}

func TestSettleStatusSendsNothingWhenTheCopyNeedsNoWrite(t *testing.T) {
	api := newFakeAPI()
	object := newThing("a", "5", 1)
	object.Status.Phase = "Ready"

	wrote, err := SettleStatus[thing](testClient(t, api), memo.New(), thingPath("a"), &object, ready)

	if wrote || err != nil || len(api.sent()) != 0 {
		t.Errorf("SettleStatus = %v, %v, and sent requests; want nothing", wrote, err)
	}
}
