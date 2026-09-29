package memo

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

type recordMeta struct {
	Name            string `json:"name"`
	Namespace       string `json:"namespace,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

func (m *recordMeta) GetName() string            { return m.Name }
func (m *recordMeta) GetNamespace() string       { return m.Namespace }
func (m *recordMeta) GetResourceVersion() string { return m.ResourceVersion }

// record is an operator's own struct for a test kind with a status.
type record struct {
	Metadata recordMeta `json:"metadata"`
	Status   struct {
		Phase string `json:"phase,omitempty"`
	} `json:"status"`
}

func (r *record) GetObjectMeta() Meta { return &r.Metadata }

func newRecord(name, version string) record {
	return record{Metadata: recordMeta{Name: name, ResourceVersion: version}}
}

// recordAPI holds one record, and answers a read of it and a write of
// its status the way the API server does: a write from a copy at an
// older resourceVersion answers 409, and each write that lands makes a
// new version. With no record it answers 404, and failing answers 500
// to every request.
type recordAPI struct {
	mu       sync.Mutex
	stored   *record
	version  int
	failing  bool
	requests []string
}

func (api *recordAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.requests = append(api.requests, r.Method+" "+r.URL.Path)
	switch {
	case api.failing:
		http.Error(w, "etcd is gone", http.StatusInternalServerError)
		return
	case api.stored == nil:
		http.NotFound(w, r)
		return
	case r.Method == http.MethodPut:
		var written record
		_ = json.NewDecoder(r.Body).Decode(&written)
		if written.Metadata.ResourceVersion != api.stored.Metadata.ResourceVersion {
			w.WriteHeader(http.StatusConflict)
			return
		}
		api.version++
		api.stored.Status = written.Status
		api.stored.Metadata.ResourceVersion = fmt.Sprint(api.version)
	}
	_ = json.NewEncoder(w).Encode(api.stored)
}

// store puts a record the way another writer does.
func (api *recordAPI) store(item record) {
	api.version++
	item.Metadata.ResourceVersion = fmt.Sprint(api.version)
	api.stored = &item
}

func (api *recordAPI) client(t *testing.T) *apiclient.Client {
	t.Helper()
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	return apiclient.New(server.URL, server.Client(), "")
}

const recordPath = "/records/a"

// ready is a status that apply sets: it reports whether the copy needs
// the write, the way a pass composes a status from the copy it holds.
func ready(item *record) bool {
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
	alreadyReady := newRecord("a", "")
	alreadyReady.Status.Phase = "Ready"
	cases := []struct {
		name      string
		stored    *record
		held      string
		failing   bool
		wantWrote bool
		wantErr   string
		wantSent  []string
	}{
		{"a current copy", &record{Metadata: recordMeta{Name: "a"}}, "101", false, true, "",
			[]string{"PUT /records/a/status"}},
		{"an older copy", &record{Metadata: recordMeta{Name: "a"}}, "90", false, true, "",
			[]string{"PUT /records/a/status", "GET /records/a", "PUT /records/a/status"}},
		{"an older copy, and the fresh copy needs nothing", &alreadyReady, "90", false, false, "",
			[]string{"PUT /records/a/status", "GET /records/a"}},
		{"a copy of an object that is gone", nil, "90", false, false, apiclient.ErrNotFound.Error(),
			[]string{"PUT /records/a/status", "GET /records/a"}},
		{"a failure", nil, "90", true, false, "etcd is gone",
			[]string{"PUT /records/a/status"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := &recordAPI{version: 100, failing: c.failing}
			if c.stored != nil {
				api.store(*c.stored)
			}
			held := newRecord("a", c.held)
			versions := New()

			wrote, err := SettleStatus[record](api.client(t), versions, recordPath, &held, ready)

			if wrote != c.wantWrote || (c.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.wantErr)) {
				t.Errorf("SettleStatus = %v, %v; want %v and an error that says %q", wrote, err, c.wantWrote, c.wantErr)
			}
			if !slices.Equal(api.requests, c.wantSent) {
				t.Errorf("the write sent %v, want %v", api.requests, c.wantSent)
			}
			if wrote && !versions.Current("a", held.Metadata.ResourceVersion) {
				t.Errorf("the memo does not hold the written version %s", held.Metadata.ResourceVersion)
			}
		})
	}
}

// A write notes the version of the copy the API server answered, so a
// store's copy from before the write is not current. A write that fails
// answers no copy, and no copy is current after it.
func TestAWriteNotesTheCopyTheAPIServerAnswered(t *testing.T) {
	cases := []struct {
		name        string
		answer      error
		wantCopy    bool
		wantCurrent string
	}{
		{"a write that lands", nil, true, "8"},
		{"a write that fails", errors.New("refused"), false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			versions := New()
			written, err := Written[record](versions, "a", func() (*record, error) {
				if c.answer != nil {
					return nil, c.answer
				}
				answer := newRecord("a", "8")
				return &answer, nil
			})

			if (written != nil) != c.wantCopy || !errors.Is(err, c.answer) {
				t.Errorf("Written = %+v, %v; want a copy %v and the error %v", written, err, c.wantCopy, c.answer)
			}
			if versions.Current("a", "7") || !versions.Current("a", c.wantCurrent) {
				t.Errorf("the memo counts version 7 current, or not version %q", c.wantCurrent)
			}
		})
	}
}

// A fresh read notes the version the API server answered.
func TestAFreshReadNotesItsVersion(t *testing.T) {
	api := &recordAPI{version: 100}
	api.store(newRecord("a", ""))
	versions := New()

	fresh, err := ReadFresh[record](api.client(t), versions, "a", recordPath)

	if err != nil || fresh.Metadata.ResourceVersion != "101" || !versions.Current("a", "101") || versions.Current("a", "100") {
		t.Errorf("ReadFresh = %+v, %v; want version 101, noted", fresh, err)
	}
}

// A store key is namespace/name for a namespaced object and the name
// for a cluster-scoped one, and NamespacedPath reads a key back into
// the object's path.
func TestAKeyNamesTheNamespace(t *testing.T) {
	path := NamespacedPath(func(namespace, name string) string { return "/ns/" + namespace + "/records/" + name })
	cases := []struct {
		meta     recordMeta
		wantKey  string
		wantPath string
	}{
		{recordMeta{Name: "a"}, "a", "/ns//records/a"},
		{recordMeta{Name: "a", Namespace: "den"}, "den/a", "/ns/den/records/a"},
	}
	for _, c := range cases {
		key := Key(&c.meta)
		if key != c.wantKey || path(key) != c.wantPath {
			t.Errorf("Key(%+v) = %q with path %q, want %q and %q", c.meta, key, path(key), c.wantKey, c.wantPath)
		}
	}
}
