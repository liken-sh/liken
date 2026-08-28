package main

// These tests cover the two resources' own client: the create that
// carries an empty spec, the status write that goes to the
// subresource, and the watch that turns a spec somebody edited into
// one wake. They run against a small API server that holds the Sinks
// and the Sources this operator writes.

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// endpointAPI is an API server that holds one collection of each
// kind. It records every request, because a pass that writes nothing
// is one of the outcomes these tests assert.
type endpointAPI struct {
	// The handler answers on the API server's own goroutines, and the
	// watch holds two of them open at once, so every read and write of
	// what the fixture holds takes the lock.
	mutex    sync.Mutex
	sinks    map[string]*Sink
	sources  map[string]*Source
	requests []string
	// watches is what the watch handler holds open, so a test can
	// close a connection and see the operator open another.
	watches chan struct{}
}

func newEndpointAPI() *endpointAPI {
	return &endpointAPI{
		sinks:   map[string]*Sink{},
		sources: map[string]*Source{},
		watches: make(chan struct{}, 8),
	}
}

func (a *endpointAPI) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mutex.Lock()
		a.requests = append(a.requests, r.Method+" "+r.URL.Path)
		a.mutex.Unlock()
		if r.URL.Query().Get("watch") == "true" {
			a.watches <- struct{}{}
			<-r.Context().Done()
			return
		}
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
			list.Items = append(list.Items, *sink)
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
		stored.Metadata.ResourceVersion = "1"
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
			list.Items = append(list.Items, *source)
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
		stored.Metadata.ResourceVersion = "1"
		a.sources[stored.Metadata.Name] = stored
		_ = json.NewEncoder(w).Encode(stored)
	default:
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}
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

// The status write goes to the status subresource, so a spec a person
// edited between the read and the write is not overwritten.
func TestStatusWritesGoToTheSubresource(t *testing.T) {
	api := newEndpointAPI()
	client := testClient(t, api.handler(t))
	sink, err := createSink(client, testSinkName)
	if err != nil {
		t.Fatal(err)
	}

	written, err := writeSinkStatus(client, sink, EndpointStatus{Node: "liken-1"})
	if err != nil {
		t.Fatal(err)
	}
	if written.Status.Node != "liken-1" {
		t.Errorf("status = %+v", written.Status)
	}
	want := "PUT " + SinksPath + "/" + testSinkName + "/status"
	if got := api.requests[len(api.requests)-1]; got != want {
		t.Errorf("request = %q, want %q", got, want)
	}
}

func TestListReadsBothCollections(t *testing.T) {
	api := newEndpointAPI()
	client := testClient(t, api.handler(t))
	if _, err := createSink(client, testSinkName); err != nil {
		t.Fatal(err)
	}
	if _, err := createSource(client, testSourceName); err != nil {
		t.Fatal(err)
	}

	sinks, err := listSinks(client)
	if err != nil {
		t.Fatal(err)
	}
	if len(sinks) != 1 || sinks[0].Metadata.Name != testSinkName {
		t.Errorf("sinks = %+v", sinks)
	}
	sources, err := listSources(client)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Metadata.Name != testSourceName {
		t.Errorf("sources = %+v", sources)
	}
}

// The watch is on both collections, because a Role can grant one
// without the other and an operator that watched one alone would
// answer a declaration on a microphone only at the backstop tick.
func TestWatchOpensBothCollections(t *testing.T) {
	api := newEndpointAPI()
	client := testClient(t, api.handler(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watchEndpoints(ctx, client, func() {})

	opened := map[string]bool{}
	for range 2 {
		select {
		case <-api.watches:
		case <-time.After(5 * time.Second):
			t.Fatal("the watch did not open both collections")
		}
	}
	api.mutex.Lock()
	requests := slices.Clone(api.requests)
	api.mutex.Unlock()
	for _, request := range requests {
		opened[request] = true
	}
	for _, path := range []string{"GET " + SinksPath, "GET " + SourcesPath} {
		if !opened[path] {
			t.Errorf("the watch opened %v, want %s among them", requests, path)
		}
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
