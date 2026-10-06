// Package eventstest is a fake of the core/v1 events collection, for
// a test's fake API server. Every component's tests read the Events
// that events.Recorder wrote from it, so no component keeps its own
// copy of an Event store. A test serves it, with the rest of its fake,
// through apiservertest, so the test runs in a synctest bubble.
package eventstest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"sync"

	"github.com/liken-sh/liken/kubernetes/events"
)

// eventPath matches the path of the events collection of a namespace,
// and of one Event in it.
var eventPath = regexp.MustCompile(`^/api/v1/namespaces/[^/]+/events(/[^/]+)?$`)

// Events holds the Events a test's fake API server receives. It
// answers a create and a merge patch the way the API server does: a
// create names the Event from its generateName, a patch of an Event
// the server does not hold answers 404, and an Event about a
// cluster-scoped object is refused outside default and kube-system.
// The zero value holds no Events and is ready to serve.
type Events struct {
	mu       sync.Mutex
	held     []events.Event
	created  int
	refusals int
	mux      *http.ServeMux
}

// Around answers a handler that serves each Event request itself and
// hands every other request to next, the test's own fake:
//
//	recorded := &eventstest.Events{}
//	server := apiservertest.Start(t, recorded.Around(fake))
func (e *Events) Around(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if eventPath.MatchString(r.URL.Path) {
			e.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ServeHTTP answers one request to the events collection.
func (e *Events) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	if e.mux == nil {
		e.mux = http.NewServeMux()
		e.mux.HandleFunc("POST /api/v1/namespaces/{namespace}/events", e.create)
		e.mux.HandleFunc("PATCH /api/v1/namespaces/{namespace}/events/{name}", e.patch)
	}
	mux := e.mux
	e.mu.Unlock()
	mux.ServeHTTP(w, r)
}

// Refuse makes the server answer the next count writes with 503, the
// way an API server that restarts does.
func (e *Events) Refuse(count int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.refusals = count
}

// Expire deletes every Event, the way the API server's TTL does an
// hour after an Event's last write.
func (e *Events) Expire() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.held = nil
}

// List answers every Event the server holds, in the order they were
// created.
func (e *Events) List() []events.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.held)
}

// About answers the Events about the object of one kind and name, in
// the order they were created.
func (e *Events) About(kind, name string) []events.Event {
	var out []events.Event
	for _, event := range e.List() {
		if event.InvolvedObject.Kind == kind && event.InvolvedObject.Name == name {
			out = append(out, event)
		}
	}
	return out
}

// refused answers whether the server refuses this write. The caller
// holds e.mu.
func (e *Events) refused(w http.ResponseWriter) bool {
	if e.refusals == 0 {
		return false
	}
	e.refusals--
	http.Error(w, "the API server is restarting", http.StatusServiceUnavailable)
	return true
}

func (e *Events) create(w http.ResponseWriter, r *http.Request) {
	var event events.Event
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	namespace := r.PathValue("namespace")
	involved := event.InvolvedObject.Namespace
	switch {
	case event.Metadata.Namespace != namespace:
		http.Error(w, "the namespace of the Event does not match the namespace of the request", http.StatusBadRequest)
		return
	case involved == "" && namespace != "default" && namespace != "kube-system":
		http.Error(w, "an Event about a cluster-scoped object must be in default or kube-system", http.StatusUnprocessableEntity)
		return
	case involved != "" && involved != namespace:
		http.Error(w, "the involved object's namespace does not match the Event's", http.StatusUnprocessableEntity)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.refused(w) {
		return
	}
	e.created++
	if event.Metadata.Name == "" {
		event.Metadata.Name = event.Metadata.GenerateName + fmt.Sprintf("%05x", e.created)
	}
	event.Metadata.ResourceVersion = strconv.Itoa(e.created)
	e.held = append(e.held, event)
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(event)
}

func (e *Events) patch(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil || r.Header.Get("Content-Type") != "application/merge-patch+json" {
		http.Error(w, "the fake answers a JSON merge patch", http.StatusUnsupportedMediaType)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.refused(w) {
		return
	}
	at := slices.IndexFunc(e.held, func(held events.Event) bool {
		return held.Metadata.Namespace == r.PathValue("namespace") && held.Metadata.Name == r.PathValue("name")
	})
	if at < 0 {
		http.Error(w, "the Event does not exist", http.StatusNotFound)
		return
	}
	var document map[string]any
	held, _ := json.Marshal(e.held[at])
	_ = json.Unmarshal(held, &document)
	merged, _ := json.Marshal(mergePatch(document, patch))
	var event events.Event
	if err := json.Unmarshal(merged, &event); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	e.held[at] = event
	_ = json.NewEncoder(w).Encode(event)
}

// mergePatch applies a JSON merge patch (RFC 7386) to a document: a
// null removes a field, an object merges into the object it replaces,
// and any other value replaces the field.
func mergePatch(document, patch map[string]any) map[string]any {
	for key, value := range patch {
		inner, isObject := value.(map[string]any)
		held, heldObject := document[key].(map[string]any)
		switch {
		case value == nil:
			delete(document, key)
		case isObject && heldObject:
			document[key] = mergePatch(held, inner)
		default:
			document[key] = value
		}
	}
	return document
}
