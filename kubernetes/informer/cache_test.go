package informer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// fakeAPI holds things by key, and answers a read of one and a write of
// its status the way the API server does: a write from a copy at an
// older resourceVersion answers 409, and each write that lands makes a
// new version. failing answers 500 to every request.
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

func thingPath(key string) string { return "/things/" + key }

func (api *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.requests = append(api.requests, r.Method+" "+r.URL.Path)
	if api.failing {
		http.Error(w, "etcd is gone", http.StatusInternalServerError)
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

// put changes a thing the way another writer does, and answers the
// copy the API server holds now.
func (api *fakeAPI) put(item thing) thing {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.version++
	item.Metadata.ResourceVersion = fmt.Sprint(api.version)
	api.objects[Key(&item.Metadata)] = item
	return item
}

// sent answers the requests the API server received, and forgets them.
func (api *fakeAPI) sent() []string {
	api.mu.Lock()
	defer api.mu.Unlock()
	requests := api.requests
	api.requests = nil
	return requests
}

func testClient(t *testing.T, api *fakeAPI) *apiclient.Client {
	t.Helper()
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	return apiclient.New(server.URL, server.Client(), "")
}

// storeOf is a store that holds the objects, the way a watch's store
// holds them once its first read is done.
func storeOf(t *testing.T, objects ...any) View {
	t.Helper()
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	for _, object := range objects {
		if item, ok := object.(thing); ok {
			object = asObject(t, item)
		}
		if err := store.Add(object); err != nil {
			t.Fatal(err)
		}
	}
	return View{Store: store, Synced: func() bool { return true }}
}

// mistyped is an object whose spec does not convert to a thing.
func mistyped(t *testing.T, name string) *unstructured.Unstructured {
	t.Helper()
	object := asObject(t, newThing(name, "5", 1))
	if err := unstructured.SetNestedField(object.Object, "large", "spec", "size"); err != nil {
		t.Fatal(err)
	}
	return object
}

// The store answers a copy it holds that converts, and nothing
// otherwise, so the caller reads the API server.
func TestAStoreAnswersACopyItHolds(t *testing.T) {
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
				t.Errorf("Cached(%s) = %+v, %v; want an answer: %v", c.key, got, ok, c.wantOK)
			}
		})
	}
}

// A list from the store is in the order of the keys, the order of a
// list from the API server, and leaves out a copy that does not convert.
func TestAListFromTheStoreIsInKeyOrder(t *testing.T) {
	view := storeOf(t, newThing("c", "5", 1), mistyped(t, "b"), newThing("a", "6", 1))

	var names []string
	for _, item := range CachedList[thing](view) {
		names = append(names, item.Metadata.Name)
	}

	if !slices.Equal(names, []string{"a", "c"}) {
		t.Errorf("CachedList = %v, want [a c]", names)
	}
}

// A read answers the store's copy while it is current, and reads the
// API server when the store holds no copy, a copy older than the
// operator's own last write, or a copy that no watch keeps current.
func TestAReadGoesToTheAPIServerOnlyWhenTheStoreCannotAnswer(t *testing.T) {
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
				t.Errorf("the memo does not hold version %s", c.wantVersion)
			}
		})
	}
}

// A list answers each current copy from the store, reads each older
// copy from the API server, and leaves out an object the API server no
// longer holds.
func TestAListReplacesEachOlderCopy(t *testing.T) {
	api := newFakeAPI()
	api.put(newThing("a", "", 1))
	newer := api.put(newThing("b", "", 2))
	view := storeOf(t, newThing("a", "101", 1), newThing("b", "90", 1), newThing("c", "91", 1))
	versions := memo.New()
	versions.Note("b", "")
	versions.Note("c", "")

	list, err := CurrentList[thing](testClient(t, api), Held{View: view, Versions: versions}, thingPath)

	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Metadata.ResourceVersion != "101" || list[1].Metadata.ResourceVersion != newer.Metadata.ResourceVersion {
		t.Errorf("the list = %+v, want a at 101 and b at %s", list, newer.Metadata.ResourceVersion)
	}
	if reads := api.sent(); !slices.Equal(reads, []string{"GET /things/b", "GET /things/c"}) {
		t.Errorf("the list sent %v, want reads of b and c", reads)
	}
}

// A list that cannot read an older copy fails, so a pass does not act
// on a list with an object missing.
func TestAListThatCannotReadAnOlderCopyFails(t *testing.T) {
	api := newFakeAPI()
	api.failing = true
	versions := memo.New()
	versions.Note("a", "")

	_, err := CurrentList[thing](testClient(t, api), Held{View: storeOf(t, newThing("a", "5", 1)), Versions: versions}, thingPath)

	if err == nil || !strings.Contains(err.Error(), "etcd is gone") {
		t.Errorf("err = %v, want the API server's failure", err)
	}
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
// list. A pass reads such a list to decide whether an object exists,
// and a list that left it out would make the pass create it again.
func TestAListAnswersACreateThatArrivesDuringTheList(t *testing.T) {
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

// ready is a status that apply sets: it reports whether the copy needs
// the write, the way a pass composes a status from the copy it holds.
func ready(item *thing) bool {
	if item.Status.Phase == "Ready" {
		return false
	}
	item.Status.Phase = "Ready"
	return true
}

// A status write lands from a current copy. A write from an older copy
// reads the object again and writes once more when the fresh copy
// still needs it. A copy of an object that is gone answers
// apiclient.ErrNotFound.
func TestAStatusWriteSettlesOnTheAPIServersCopy(t *testing.T) {
	alreadyReady := newThing("a", "", 2)
	alreadyReady.Status.Phase = "Ready"
	cases := []struct {
		name      string
		stored    *thing
		held      string
		failing   bool
		wantWrote bool
		wantErr   string
		wantSent  []string
	}{
		{"a current copy", &thing{Metadata: thingMeta{Name: "a"}}, "101", false, true, "",
			[]string{"PUT /things/a/status"}},
		{"an older copy", &thing{Metadata: thingMeta{Name: "a"}}, "90", false, true, "",
			[]string{"PUT /things/a/status", "GET /things/a", "PUT /things/a/status"}},
		{"an older copy, and the fresh copy needs nothing", &alreadyReady, "90", false, false, "",
			[]string{"PUT /things/a/status", "GET /things/a"}},
		{"a copy of an object that is gone", nil, "90", false, false, apiclient.ErrNotFound.Error(),
			[]string{"PUT /things/a/status", "GET /things/a"}},
		{"a failure", nil, "90", true, false, "etcd is gone",
			[]string{"PUT /things/a/status"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := newFakeAPI()
			api.failing = c.failing
			if c.stored != nil {
				api.put(*c.stored)
			}
			held := newThing("a", c.held, 1)
			versions := memo.New()

			wrote, err := SettleStatus[thing](testClient(t, api), versions, thingPath("a"), &held, ready)

			if wrote != c.wantWrote || (c.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.wantErr)) {
				t.Errorf("SettleStatus = %v, %v; want %v and an error that says %q", wrote, err, c.wantWrote, c.wantErr)
			}
			if sent := api.sent(); !slices.Equal(sent, c.wantSent) {
				t.Errorf("the write sent %v, want %v", sent, c.wantSent)
			}
			if wrote && !versions.Current("a", held.Metadata.ResourceVersion) {
				t.Errorf("the memo does not hold the written version %s", held.Metadata.ResourceVersion)
			}
		})
	}
}

// A copy that needs no write sends nothing.
func TestAStatusThatNeedsNoWriteSendsNothing(t *testing.T) {
	api := newFakeAPI()
	held := newThing("a", "5", 1)
	held.Status.Phase = "Ready"

	wrote, err := SettleStatus[thing](testClient(t, api), memo.New(), thingPath("a"), &held, ready)

	if wrote || err != nil || len(api.sent()) != 0 {
		t.Errorf("SettleStatus = %v, %v, and sent requests; want nothing", wrote, err)
	}
}

// A store key is namespace/name for a namespaced object and the name
// for a cluster-scoped one.
func TestAStoreKeyNamesTheNamespace(t *testing.T) {
	cases := []struct {
		meta thingMeta
		want string
	}{
		{thingMeta{Name: "a"}, "a"},
		{thingMeta{Name: "a", Namespace: "den"}, "den/a"},
	}
	for _, c := range cases {
		if got := Key(&c.meta); got != c.want {
			t.Errorf("Key(%+v) = %q, want %q", c.meta, got, c.want)
		}
	}
}
