package main

// These tests cover the two resources' own client: the create that
// carries an empty spec, the status write that goes to the
// subresource, and the list of one machine's resources. They run
// against a small API server that holds the Sinks and the Sources this
// operator writes. endpointwatch_test.go covers the watch.

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// endpointAPI is an API server that holds one collection of each
// kind. It records every request, because a pass that writes nothing
// is one of the outcomes these tests assert.
type endpointAPI struct {
	// The handler answers on the API server's own goroutines, so every
	// read and write of what the fixture holds takes the lock.
	mutex    sync.Mutex
	sinks    map[string]*Sink
	sources  map[string]*Source
	requests []string
	// version is the last resourceVersion the fixture stored. A PUT
	// that carries another version than the stored one is answered
	// 409, the way the API server answers a write from an older copy.
	version int
}

func newEndpointAPI() *endpointAPI {
	return &endpointAPI{
		sinks:   map[string]*Sink{},
		sources: map[string]*Source{},
	}
}

func (a *endpointAPI) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mutex.Lock()
		a.requests = append(a.requests, r.Method+" "+r.URL.Path)
		a.mutex.Unlock()
		if strings.HasPrefix(r.URL.Path, SourcesPath) {
			a.serveSources(t, w, r)
			return
		}
		a.serveSinks(t, w, r)
	})
}

func (a *endpointAPI) serveSinks(t *testing.T, w http.ResponseWriter, r *http.Request) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	name := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/status"), SinksPath+"/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == SinksPath:
		list := SinkList{}
		for _, sink := range a.sinks {
			if selects(r, sink.Status) {
				list.Items = append(list.Items, *sink)
			}
		}
		_ = json.NewEncoder(w).Encode(list)
	case r.Method == http.MethodGet:
		sink, held := a.sinks[name]
		if !held {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(sink)
	case r.Method == http.MethodPost, r.Method == http.MethodPut:
		stored := &Sink{}
		_ = json.NewDecoder(r.Body).Decode(stored)
		if held, found := a.sinks[stored.Metadata.Name]; r.Method == http.MethodPut && found &&
			held.Metadata.ResourceVersion != stored.Metadata.ResourceVersion {
			w.WriteHeader(http.StatusConflict)
			return
		}
		stored.Metadata.ResourceVersion = a.nextVersion()
		a.sinks[stored.Metadata.Name] = stored
		_ = json.NewEncoder(w).Encode(stored)
	default:
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}
}

func (a *endpointAPI) serveSources(t *testing.T, w http.ResponseWriter, r *http.Request) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	name := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/status"), SourcesPath+"/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == SourcesPath:
		list := SourceList{}
		for _, source := range a.sources {
			if selects(r, source.Status) {
				list.Items = append(list.Items, *source)
			}
		}
		_ = json.NewEncoder(w).Encode(list)
	case r.Method == http.MethodGet:
		source, held := a.sources[name]
		if !held {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(source)
	case r.Method == http.MethodPost, r.Method == http.MethodPut:
		stored := &Source{}
		_ = json.NewDecoder(r.Body).Decode(stored)
		if held, found := a.sources[stored.Metadata.Name]; r.Method == http.MethodPut && found &&
			held.Metadata.ResourceVersion != stored.Metadata.ResourceVersion {
			w.WriteHeader(http.StatusConflict)
			return
		}
		stored.Metadata.ResourceVersion = a.nextVersion()
		a.sources[stored.Metadata.Name] = stored
		_ = json.NewEncoder(w).Encode(stored)
	default:
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}
}

// nextVersion answers a new resourceVersion. The caller holds the
// lock.
func (a *endpointAPI) nextVersion() string {
	a.version++
	return strconv.Itoa(a.version)
}

// selects answers whether a list's field selector takes a resource,
// the way the API server answers it for the selectable field
// status.node. A list with no selector takes every resource.
func selects(r *http.Request, status EndpointStatus) bool {
	selector := r.URL.Query().Get("fieldSelector")
	if selector == "" {
		return true
	}
	return selector == "status.node="+status.Node
}

// The create states nothing about how the endpoint rests. A spec with
// a field in it would be the operator declaring policy, and the
// resource exists so that a person can.
func TestCreateCarriesAnEmptySpec(t *testing.T) {
	api := newEndpointAPI()
	client := testClient(t, api.handler(t))

	sink, err := createSink(client, testSinkName)
	if err != nil {
		t.Fatal(err)
	}
	if sink.Metadata.Name != testSinkName {
		t.Errorf("name = %q", sink.Metadata.Name)
	}
	if !reflect.DeepEqual(sink.Spec, SinkSpec{}) {
		t.Errorf("spec = %+v, want an empty one", sink.Spec)
	}
	if got := api.requests; len(got) != 1 || got[0] != "POST "+SinksPath {
		t.Errorf("requests = %v", got)
	}

	source, err := createSource(client, testSourceName)
	if err != nil {
		t.Fatal(err)
	}
	if source.Metadata.Name != testSourceName {
		t.Errorf("name = %q", source.Metadata.Name)
	}
}

func TestListReadsThisMachinesResourcesInBothCollections(t *testing.T) {
	api := newEndpointAPI()
	client := testClient(t, api.handler(t))
	api.sinks[testSinkName] = &Sink{Metadata: EndpointMeta{Name: testSinkName},
		Status: EndpointStatus{Node: "liken-1"}}
	api.sinks["stick-1-pci-0000-00-0e-0-hdmi-0"] = &Sink{
		Metadata: EndpointMeta{Name: "stick-1-pci-0000-00-0e-0-hdmi-0"},
		Status:   EndpointStatus{Node: "stick-1"}}
	api.sources[testSourceName] = &Source{Metadata: EndpointMeta{Name: testSourceName},
		Status: EndpointStatus{Node: "liken-1"}}

	sinks, err := listSinks(client, "liken-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sinks) != 1 || sinks[0].Metadata.Name != testSinkName {
		t.Errorf("sinks = %+v", sinks)
	}
	sources, err := listSources(client, "liken-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Metadata.Name != testSourceName {
		t.Errorf("sources = %+v", sources)
	}
}

// A condition that says the same thing keeps the time it changed
// last. A timestamp that moved on every pass would make every pass a
// write, and every write reaches every reader of the resource.
func TestConditionsKeepTheirTimestampUntilTheyChange(t *testing.T) {
	first := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	later := first.Add(time.Hour)

	conditions := setCondition(nil, condition(ConnectedCondition, true, "MonitorPresent", "a monitor answers", first))
	conditions = setCondition(conditions, condition(ConnectedCondition, true, "MonitorPresent", "a monitor answers", later))
	if got := conditions[0].LastTransitionTime; got != first.Format(time.RFC3339) {
		t.Errorf("an unchanged condition moved its timestamp to %q", got)
	}

	conditions = setCondition(conditions, condition(ConnectedCondition, false, "NoMonitor", "no monitor answers", later))
	if got := conditions[0].LastTransitionTime; got != later.Format(time.RFC3339) {
		t.Errorf("a condition that changed kept the timestamp %q", got)
	}
	if len(conditions) != 1 {
		t.Errorf("conditions = %+v, want the one type", conditions)
	}
}
