package main

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

// endpointAPI is a fake API server that stores Sinks and Sources. It
// records every request, because some tests check that a pass writes
// nothing.
type endpointAPI struct {
	// The handler runs on the test server's goroutines, so mutex guards
	// every field below it.
	mutex    sync.Mutex
	sinks    map[string]*Sink
	sources  map[string]*Source
	requests []string
	// version is the last resourceVersion that the fake stored. A PUT
	// whose resourceVersion differs from the stored object's gets a 409,
	// as a write from a stale copy does from the real API server.
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

// SinkList and SourceList wrap items the way the API server returns a
// collection.
type SinkList struct {
	Items []Sink `json:"items"`
}

type SourceList struct {
	Items []Source `json:"items"`
}

func (a *endpointAPI) serveSinks(t *testing.T, w http.ResponseWriter, r *http.Request) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	name := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/status"), SinksPath+"/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == SinksPath:
		list := SinkList{}
		for _, sink := range a.sinks {
			if selects(r, sink.Status.EndpointStatus) {
				list.Items = append(list.Items, *sink)
			}
		}
		_ = json.NewEncoder(w).Encode(list)
	case r.Method == http.MethodGet:
		sink, found := a.sinks[name]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(sink)
	case r.Method == http.MethodPatch:
		a.patchSink(t, w, r, name)
	case r.Method == http.MethodPost, r.Method == http.MethodPut:
		stored := &Sink{}
		_ = json.NewDecoder(r.Body).Decode(stored)
		if current, found := a.sinks[stored.Metadata.Name]; r.Method == http.MethodPut && found &&
			current.Metadata.ResourceVersion != stored.Metadata.ResourceVersion {
			w.WriteHeader(http.StatusConflict)
			return
		}
		stored.Metadata.ResourceVersion = a.nextVersion()
		a.sinks[stored.Metadata.Name] = stored
		_ = json.NewEncoder(w).Encode(stored)
	default:
		t.Errorf("the fixture received %s %s, want a request it serves for Sinks", r.Method, r.URL.Path)
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
		source, found := a.sources[name]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(source)
	case r.Method == http.MethodPost, r.Method == http.MethodPut:
		stored := &Source{}
		_ = json.NewDecoder(r.Body).Decode(stored)
		if current, found := a.sources[stored.Metadata.Name]; r.Method == http.MethodPut && found &&
			current.Metadata.ResourceVersion != stored.Metadata.ResourceVersion {
			w.WriteHeader(http.StatusConflict)
			return
		}
		stored.Metadata.ResourceVersion = a.nextVersion()
		a.sources[stored.Metadata.Name] = stored
		_ = json.NewEncoder(w).Encode(stored)
	default:
		t.Errorf("the fixture received %s %s, want a request it serves for Sources", r.Method, r.URL.Path)
	}
}

// patchSink applies a merge patch to a Sink's status, as the API
// server does for the status subresource. A patch whose resourceVersion
// differs from the stored object's gets a 409, and fields that the patch
// omits keep their values. The caller holds the lock.
func (a *endpointAPI) patchSink(t *testing.T, w http.ResponseWriter, r *http.Request, name string) {
	current, found := a.sinks[name]
	if !found {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Header.Get("Content-Type") != mergePatchType {
		t.Errorf("a patch of %s carried the content type %q, want %q", name, r.Header.Get("Content-Type"), mergePatchType)
	}
	var patch map[string]any
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		t.Errorf("decoding the patch of %s: %v", name, err)
		return
	}
	stated, _ := patch["metadata"].(map[string]any)["resourceVersion"].(string)
	if stated != "" && stated != current.Metadata.ResourceVersion {
		w.WriteHeader(http.StatusConflict)
		return
	}
	fields, err := asFields(current)
	if err != nil {
		t.Fatalf("reading the fields of the stored Sink %s: %v", name, err)
	}
	fields["status"] = applyMergePatch(fields["status"], patch["status"])
	stored := &Sink{}
	if err := remarshal(fields, stored); err != nil {
		t.Fatalf("decoding the patched Sink %s: %v", name, err)
	}
	stored.Metadata.ResourceVersion = a.nextVersion()
	a.sinks[name] = stored
	_ = json.NewEncoder(w).Encode(stored)
}

func sinkStatus(facts EndpointStatus) SinkStatus {
	return SinkStatus{EndpointStatus: facts}
}

func declaredLevel(level int) *SinkVolume {
	return &SinkVolume{Level: pointerTo(level)}
}

// The caller of nextVersion holds the lock.
func (a *endpointAPI) nextVersion() string {
	a.version++
	return strconv.Itoa(a.version)
}

// selects reports whether the request's fieldSelector matches the
// status, as the API server filters on the selectable field
// status.node. A request with no selector matches every resource.
func selects(r *http.Request, status EndpointStatus) bool {
	selector := r.URL.Query().Get("fieldSelector")
	if selector == "" {
		return true
	}
	return selector == "status.node="+status.Node
}

func TestCreateCarriesAnEmptySpec(t *testing.T) {
	api := newEndpointAPI()
	client := testClient(t, api.handler(t))

	sink, err := createSink(client, testSinkName)
	if err != nil {
		t.Fatalf("creating the Sink: %v", err)
	}
	if sink.Metadata.Name != testSinkName {
		t.Errorf("the created Sink is named %q, want %q", sink.Metadata.Name, testSinkName)
	}
	if !reflect.DeepEqual(sink.Spec, SinkSpec{}) {
		t.Errorf("spec = %+v, want an empty one", sink.Spec)
	}
	if got := api.requests; len(got) != 1 || got[0] != "POST "+SinksPath {
		t.Errorf("the fixture received %v, want one POST %s", got, SinksPath)
	}

	source, err := createSource(client, testSourceName)
	if err != nil {
		t.Fatalf("creating the Source: %v", err)
	}
	if source.Metadata.Name != testSourceName {
		t.Errorf("the created Source is named %q, want %q", source.Metadata.Name, testSourceName)
	}
}

// A pass lists this machine's Sinks and Sources with the field selector
// that the watches use, so it never reads another machine's resources.
func TestListReadsThisMachinesResourcesInBothCollections(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	withMemos(control)
	api.sinks[testSinkName] = &Sink{Metadata: EndpointMeta{Name: testSinkName},
		Status: sinkStatus(EndpointStatus{Node: "liken-1"})}
	api.sinks["stick-1-pci-0000-00-0e-0-hdmi-0"] = &Sink{
		Metadata: EndpointMeta{Name: "stick-1-pci-0000-00-0e-0-hdmi-0"},
		Status:   sinkStatus(EndpointStatus{Node: "stick-1"})}
	api.sources[testSourceName] = &Source{Metadata: EndpointMeta{Name: testSourceName},
		Status: EndpointStatus{Node: "liken-1"}}

	sinks, err := control.readSinks()
	if err != nil {
		t.Fatalf("listing the Sinks: %v", err)
	}
	if len(sinks) != 1 || sinks[0].Metadata.Name != testSinkName {
		t.Errorf("the list returned the Sinks %+v, want only %s", sinks, testSinkName)
	}
	sources, err := control.readSources()
	if err != nil {
		t.Fatalf("listing the Sources: %v", err)
	}
	if len(sources) != 1 || sources[0].Metadata.Name != testSourceName {
		t.Errorf("the list returned the Sources %+v, want only %s", sources, testSourceName)
	}
}

// A condition whose status is unchanged keeps its lastTransitionTime.
// Otherwise every pass would write the status, and each write notifies
// every watcher of the resource.
func TestConditionsKeepTheirTimestampUntilTheyChange(t *testing.T) {
	first := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	later := first.Add(time.Hour)

	conditions := setCondition(nil, condition(ConnectedCondition, true, "MonitorPresent", "a monitor answers", first))
	conditions = setCondition(conditions, condition(ConnectedCondition, true, "MonitorPresent", "a monitor answers", later))
	if got := conditions[0].LastTransitionTime; !got.Equal(first) {
		t.Errorf("an unchanged condition moved its timestamp to %s, want %s", got, first)
	}

	conditions = setCondition(conditions, condition(ConnectedCondition, false, "NoMonitor", "no monitor answers", later))
	if got := conditions[0].LastTransitionTime; !got.Equal(later) {
		t.Errorf("a condition that changed has the timestamp %s, want %s", got, later)
	}
	if len(conditions) != 1 {
		t.Errorf("conditions = %+v, want one Connected condition", conditions)
	}
}
