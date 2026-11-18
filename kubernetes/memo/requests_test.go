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

// record is a test kind with a status, like an operator's own struct.
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

// recordAPI serves one record like the API server: a status write with
// an old resourceVersion gets 409, and each successful write sets a new
// version. With no record it returns 404, and while failing is true,
// every request gets 500.
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

// store writes a record as another writer would.
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

// ready is an apply function for SettleStatus that sets the Ready phase.
func ready(item *record) bool {
	if item.Status.Phase == "Ready" {
		return false
	}
	item.Status.Phase = "Ready"
	return true
}

func TestSettleStatusRetriesOnTheAPIServersCopy(t *testing.T) {
	alreadyReady := newRecord("a", "")
	alreadyReady.Status.Phase = "Ready"
	cases := []struct {
		name      string
		stored    *record
		version   string
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
			object := newRecord("a", c.version)
			versions := New()

			wrote, err := SettleStatus[record](api.client(t), versions, recordPath, &object, ready)

			if wrote != c.wantWrote || (c.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.wantErr)) {
				t.Errorf("SettleStatus = %v, %v; want %v and an error containing %q", wrote, err, c.wantWrote, c.wantErr)
			}
			if !slices.Equal(api.requests, c.wantSent) {
				t.Errorf("the write sent %v, want %v", api.requests, c.wantSent)
			}
			if wrote && !versions.Current("a", object.Metadata.ResourceVersion) {
				t.Errorf("the memo has no record of version %s, the one that was written", object.Metadata.ResourceVersion)
			}
		})
	}
}

// After a write, a store copy from before the write is not current.
func TestWrittenRecordsTheReturnedVersion(t *testing.T) {
	cases := []struct {
		name       string
		requestErr error
		wantCopy   bool
	}{
		{"a write that succeeds", nil, true},
		{"a write that fails", errors.New("refused"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			versions := New()
			written, err := Written[record](versions, "a", func() (*record, error) {
				if c.requestErr != nil {
					return nil, c.requestErr
				}
				object := newRecord("a", "8")
				return &object, nil
			})

			if (written != nil) != c.wantCopy || !errors.Is(err, c.requestErr) {
				t.Errorf("Written = %+v, %v; want a copy %v and the error %v", written, err, c.wantCopy, c.requestErr)
			}
			if versions.Current("a", "7") || versions.Current("a", "8") != c.wantCopy {
				t.Errorf("Current(a, 8) = %v, want %v, and Current(a, 7) = false", versions.Current("a", "8"), c.wantCopy)
			}
		})
	}
}

func TestReadFreshRecordsItsVersion(t *testing.T) {
	api := &recordAPI{version: 100}
	api.store(newRecord("a", ""))
	versions := New()

	fresh, err := ReadFresh[record](api.client(t), versions, "a", recordPath)

	if err != nil || fresh.Metadata.ResourceVersion != "101" || !versions.Current("a", "101") || versions.Current("a", "100") {
		t.Errorf("ReadFresh = %+v, %v; want version 101, recorded in the memo", fresh, err)
	}
}

func TestReadListRecordsEachObjectsVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []record{newRecord("a", "7"), newRecord("b", "8")}})
	}))
	t.Cleanup(server.Close)
	versions := New()

	list, err := ReadList[record](apiclient.New(server.URL, server.Client(), ""), versions, "/records")

	if err != nil || len(list) != 2 || list[0].Metadata.Name != "a" || list[1].Metadata.Name != "b" {
		t.Fatalf("ReadList = %+v, %v; want a and b", list, err)
	}
	if !versions.Current("a", "7") || versions.Current("a", "6") || !versions.Current("b", "8") || versions.Current("b", "6") {
		t.Error("the memo does not record each object at its listed version")
	}
}

func TestKeyAndNamespacedPathRoundTripNamespacedAndClusterScopedObjects(t *testing.T) {
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

// plainRecord is like a type from a package that does not import memo.
// Its method returns the interface literal that Meta aliases.
type plainRecord struct {
	Metadata recordMeta `json:"metadata"`
}

func (r *plainRecord) GetObjectMeta() interface {
	GetNamespace() string
	GetName() string
	GetResourceVersion() string
} {
	return &r.Metadata
}

func TestATypeThatNeverNamesMetaSatisfiesObject(t *testing.T) {
	versions := New()
	written, err := Written[plainRecord](versions, "a", func() (*plainRecord, error) {
		return &plainRecord{Metadata: recordMeta{Name: "a", ResourceVersion: "7"}}, nil
	})
	if err != nil || written.Metadata.ResourceVersion != "7" || !versions.Current("a", "7") {
		t.Errorf("Written = %+v, %v; want the copy at 7, recorded in the memo", written, err)
	}
}
